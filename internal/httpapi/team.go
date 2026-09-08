package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/team"
)

// 名簿の読み書き (#731906)。
//
// 共通の定義に対する操作 (agents.go) と入力の形は同じで、書き先だけが会話の
// 下になる。検証も同じものを通す。エージェントであることに変わりはないので、
// 別の形式や別の検証を持ち込まない。

// memberBody は名簿の 1 人。共通か固有かを添える。画面はこれで欄を振り分ける。
type memberBody struct {
	*agent.Agent
	Local bool `json:"local"`
	Lead  bool `json:"lead"`
}

// rosterBody は名簿の応答。参加していない共通エージェントも一緒に返す。
//
// 2 回問い合わせる形にすると、片方だけ古い一覧を描く瞬間が生まれる。
type rosterBody struct {
	Members   []memberBody      `json:"members"`
	Available []*agent.Agent    `json:"available"`
	LeadID    string            `json:"lead_id"`
	Errors    []agent.LoadError `json:"errors,omitempty"`
}

// ErrNotTeam は直列の会話にチームの操作を求めたこと。呼ぶ側の誤りなので、
// 障害と同じ扱いにしない。まとめて 500 にすると、画面は直せる誤りと直せない
// 故障を区別できない。
var ErrNotTeam = errors.New("この会話はチームセッションではありません")

// teamSession は会話を引き、チームであることを確かめる。
func (s *Server) teamSession(r *http.Request) (*store.Session, error) {
	sess, err := s.st.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if sess.Kind != config.KindTeam {
		return nil, ErrNotTeam
	}
	return sess, nil
}

func (s *Server) handleRoster(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.rosterOf(sess))
}

func (s *Server) rosterOf(sess *store.Session) rosterBody {
	roster := team.Load(s.cfg, s.agents, sess)
	body := rosterBody{Members: []memberBody{}, Available: []*agent.Agent{},
		LeadID: roster.LeadID, Errors: roster.Errors}

	in := map[string]bool{}
	for _, m := range roster.Members {
		in[m.ID] = true
		body.Members = append(body.Members,
			memberBody{Agent: m.Agent, Local: m.Local, Lead: m.ID == roster.LeadID})
	}
	for _, a := range s.agents.List() {
		if !in[a.ID] {
			body.Available = append(body.Available, a)
		}
	}
	return body
}

// canAnswer はその会話でそのエージェントを窓口 (直列なら答え手) にできるかを返す。
//
// チームでは名簿から引く。共通の一覧だけを見ると、この会話のために作った
// 固有のエージェントを窓口にできない。窓口を移せなければ規定エージェントも
// 外せず、名簿は general に縛られたままになる (#731906)。
func (s *Server) canAnswer(r *http.Request, agentID string) error {
	sess, err := s.st.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if sess.Kind == config.KindTeam {
		roster := team.Load(s.cfg, s.agents, sess)
		if _, ok := roster.Get(agentID); !ok {
			return &agent.FieldError{Field: "agent_id", Reason: fmt.Sprintf(
				"%q はこの会話に居ません。先に名簿へ加えてください", agentID)}
		}
		return nil
	}
	if _, ok := s.agents.Get(agentID); !ok {
		return &agent.FieldError{Field: "agent_id",
			Reason: "エージェント " + agentID + " の定義が見つかりません"}
	}
	return nil
}

// handleJoin は共通エージェントの参加を切り替える。
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	var in struct {
		AgentID string `json:"agent_id"`
		Join    bool   `json:"join"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, ok := s.agents.Get(in.AgentID); !ok {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("エージェント %q の定義が見つかりません", in.AgentID))
		return
	}
	// 窓口は外せない。外すなら先に窓口を移す。宛先の無い発言の行き先が
	// 消えると、その会話は何も受け取れなくなる。
	if !in.Join && in.AgentID == sess.AgentID {
		writeError(w, http.StatusConflict,
			errors.New("窓口は外せません。先に窓口を別のメンバーへ移してください"))
		return
	}

	next := make([]string, 0, len(sess.Members)+1)
	for _, id := range sess.Members {
		if id != in.AgentID {
			next = append(next, id)
		}
	}
	if in.Join {
		next = append(next, in.AgentID)
	}
	if err := s.st.SetMembers(r.Context(), sess.ID, next); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// handleCreateSessionAgent はこの会話だけのエージェントを作る。
func (s *Server) handleCreateSessionAgent(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	var b agentBody
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.freeInSession(sess, b.ID); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	a := &agent.Agent{ID: b.ID}
	b.intoLocal(a)
	if err := agent.SaveLocal(s.cfg.SessionAgentsDir(sess.ID), a); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// handleUpdateSessionAgent は固有の定義を書き戻す。
func (s *Server) handleUpdateSessionAgent(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	cur, err := s.localAgent(sess, r.PathValue("aid"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	var b agentBody
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	next := *cur
	b.intoLocal(&next)
	if err := agent.SaveLocal(s.cfg.SessionAgentsDir(sess.ID), &next); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// handleDeleteSessionAgent は固有の定義を消す。過去の発言には触れない。
// 消すと、残っているメッセージの送り手が誰なのか分からなくなる。
func (s *Server) handleDeleteSessionAgent(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	id := r.PathValue("aid")
	if id == sess.AgentID {
		writeError(w, http.StatusConflict,
			errors.New("窓口は消せません。先に窓口を別のメンバーへ移してください"))
		return
	}
	a, err := s.localAgent(sess, id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if err := os.Remove(a.File); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// handleCopyAgent は共通エージェントを写して固有の定義にする。
//
// 写したあとは元と縁が切れる。以後どちらを直しても、もう片方は変わらない。
func (s *Server) handleCopyAgent(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	var in struct {
		AgentID string `json:"agent_id"`
		ID      string `json:"id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	src, ok := s.agents.Get(in.AgentID)
	if !ok {
		writeError(w, http.StatusNotFound,
			fmt.Errorf("エージェント %q の定義が見つかりません", in.AgentID))
		return
	}
	if in.ID == "" {
		in.ID = s.freeCopyID(sess, src.ID)
	}
	if err := s.freeInSession(sess, in.ID); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}

	next := *src
	next.ID = in.ID
	next.File = ""
	next.Fixed = false
	// 規定エージェントの写しは Tier 0 のままでよい。対等な 2 人組は正当な
	// 構成であり、同位どうしは依頼しかできないという規則がそれを支える。
	if err := agent.SaveLocal(s.cfg.SessionAgentsDir(sess.ID), &next); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// freeInSession はその ID を会話の中で使えるかを返す。
//
// 一意性を会話の中に閉じるのは、メンションの宛先が ID だからである。同じ
// 会話に同じ名前が 2 つあると、宛先が誰を指すのか決められない。
func (s *Server) freeInSession(sess *store.Session, id string) error {
	if err := agent.ValidateID(id); err != nil {
		return err
	}
	if _, ok := s.agents.Get(id); ok {
		return &agent.FieldError{Field: "id",
			Reason: fmt.Sprintf("ID %q は共通エージェントが使っています。この会話の中では別の名前にしてください", id)}
	}
	locals, _ := agent.ReadDir(s.cfg.SessionAgentsDir(sess.ID))
	for _, a := range locals {
		if a.ID == id {
			return &agent.FieldError{Field: "id",
				Reason: fmt.Sprintf("ID %q はこの会話で既に使われています", id)}
		}
	}
	return nil
}

// freeCopyID は写しに付ける既定の名前。元の名前は共通が使っているので、
// そのままでは会話の中で衝突する。
func (s *Server) freeCopyID(sess *store.Session, base string) string {
	for i := 2; i < 100; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if s.freeInSession(sess, id) == nil {
			return id
		}
	}
	return base + "-" + store.NewID()[:6]
}

func (s *Server) localAgent(sess *store.Session, id string) (*agent.Agent, error) {
	locals, _ := agent.ReadDir(s.cfg.SessionAgentsDir(sess.ID))
	for _, a := range locals {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("%w: この会話のエージェント %q", store.ErrNotFound, id)
}

func (s *Server) writeRoster(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := s.st.GetSession(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.rosterOf(sess))
}

// removeSessionAgents は会話を消すときに固有の定義も消す。
//
// 定義はその会話のために作られたもので、会話が無くなれば読み手が居ない。
// 作業ディレクトリを消さないこと (#903215) とは扱いが違う — あちらは利用者が
// 置いた材料と成果物であり、こちらは会話の一部である。
func (s *Server) removeSessionAgents(_ context.Context, id string) {
	dir := s.cfg.SessionAgentsDir(id)
	if dir == "" {
		return
	}
	_ = os.RemoveAll(dir)
}
