package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/team"
)

// 名簿と、チームエージェントの定義 (#731906)。
//
// 定義は共通と同じ形式・同じ検証で読み書きする。エージェントであることに
// 変わりはないので、別の形式や別の検証を持ち込まない。
//
// **チームエージェントは全てのチーム会話で共有される。**だから定義を直す
// 経路は会話 ID の下に置いていない。会話の下にあると、別の会話から同じ
// ファイルを書けることが経路から読めず、「この会話のもの」を編集していると
// 誤解させ続ける。会話の下に残すのは作成とコピーで、どちらも「定義を作る」と
// 「この会話で有効にする」を 1 度に行う操作である。

// memberBody は名簿の 1 人。由来を添える。画面はこれで欄を振り分ける。
type memberBody struct {
	*agent.Agent
	Scope string `json:"scope"`
}

// rosterBody は名簿の応答。有効にしてあるものと、まだのものを 1 度で返す。
//
// 2 回問い合わせる形にすると、片方だけ古い一覧を描く瞬間が生まれる。
type rosterBody struct {
	Members   []memberBody      `json:"members"`
	Available []memberBody      `json:"available"`
	Errors    []agent.LoadError `json:"errors,omitempty"`
}

// ErrNotTeam は直列の会話にチームの操作を求めたこと。呼ぶ側の誤りなので、
// 障害と同じ扱いにしない。
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

// handleFlow は連絡の記録 (流れ図) を返す (#512740)。
//
// 図の組み立ては team.BuildFlow に置いてある。画面と指示文が同じものを二度
// 組み立てると、食い違ったときにどちらが正か決められない。
func (s *Server) handleFlow(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	edges, err := s.st.TeamFlow(r.Context(), sess.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, team.BuildFlow(team.Load(s.agents, s.teamAgents, sess), edges))
}

func (s *Server) rosterOf(sess *store.Session) rosterBody {
	roster := team.Load(s.agents, s.teamAgents, sess)
	body := rosterBody{Members: []memberBody{}, Available: []memberBody{}, Errors: roster.Errors}

	on := map[string]bool{}
	for _, m := range roster.Members {
		on[m.ID] = true
		body.Members = append(body.Members, memberBody{Agent: m.Agent, Scope: m.Scope})
	}
	for _, a := range s.agents.List() {
		if !on[a.ID] {
			body.Available = append(body.Available, memberBody{Agent: a, Scope: team.ScopeCommon})
		}
	}
	for _, a := range s.teamAgents.List() {
		if !on[a.ID] {
			body.Available = append(body.Available, memberBody{Agent: a, Scope: team.ScopeTeam})
		}
	}
	return body
}

// handleEnable はこの会話でエージェントを有効にする / 外す。
//
// 定義そのものには触れない。ここで変わるのは「この会話で使うかどうか」だけで、
// それは会話に属する。
func (s *Server) handleEnable(w http.ResponseWriter, r *http.Request) {
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
	if _, ok := s.anyAgent(in.AgentID); !ok {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("エージェント %q の定義が見つかりません", in.AgentID))
		return
	}
	if err := s.setEnabled(r, sess, in.AgentID, in.Join); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// setEnabled は名簿への出し入れ。有効にするときは末尾へ足す。
func (s *Server) setEnabled(r *http.Request, sess *store.Session, id string, on bool) error {
	next := make([]string, 0, len(sess.Members)+1)
	for _, cur := range sess.Members {
		if cur != id {
			next = append(next, cur)
		}
	}
	if on {
		next = append(next, id)
	}
	return s.st.SetMembers(r.Context(), sess.ID, next)
}

// anyAgent は共通とチームの両方から引く。ID は全体で一意なので、どちらから
// 来たかは ID だけで決まる。
func (s *Server) anyAgent(id string) (*agent.Agent, bool) {
	if a, ok := s.agents.Get(id); ok {
		return a, true
	}
	return s.teamAgents.Get(id)
}

// handleCreateTeamAgent はチームエージェントを作り、この会話で有効にする。
//
// 作ってから「有効化」をもう一度押させる形にはしない。パネルから人を足したのに
// この会話に居ないのは、誰の期待とも合わない。
func (s *Server) handleCreateTeamAgent(w http.ResponseWriter, r *http.Request) {
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
	if err := s.freeAgentID(b.ID); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	a := &agent.Agent{ID: b.ID}
	b.intoTeam(a)
	if err := agent.SaveTeam(s.cfg.TeamAgentDir(), a); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.reloadTeamAgents()
	if err := s.setEnabled(r, sess, b.ID, true); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// handleCopyAgent は共通エージェントを写してチームエージェントにし、この
// 会話で有効にする。写したあとは元と縁が切れる。
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
	src, ok := s.anyAgent(in.AgentID)
	if !ok {
		writeError(w, http.StatusNotFound,
			fmt.Errorf("エージェント %q の定義が見つかりません", in.AgentID))
		return
	}
	if in.ID == "" {
		in.ID = s.freeCopyID(src.ID)
	}
	if err := s.freeAgentID(in.ID); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}

	next := *src
	next.ID = in.ID
	next.File = ""
	next.Fixed = false
	if err := agent.SaveTeam(s.cfg.TeamAgentDir(), &next); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.reloadTeamAgents()
	if err := s.setEnabled(r, sess, in.ID, true); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.writeRoster(w, r, sess.ID)
}

// handleUpdateTeamAgent は定義を書き戻す。
//
// 会話 ID の下に無いのは、これが共有物への操作だからである。直せば、有効に
// している全ての会話に効く。
func (s *Server) handleUpdateTeamAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cur, ok := s.teamAgents.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound,
			fmt.Errorf("チームエージェント %q が見つかりません", id))
		return
	}
	var b agentBody
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	next := *cur
	b.intoTeam(&next)
	if err := agent.SaveTeam(s.cfg.TeamAgentDir(), &next); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.reloadTeamAgents()
	saved, ok := s.teamAgents.Get(id)
	if !ok {
		writeError(w, http.StatusInternalServerError,
			fmt.Errorf("保存した定義 %q を読み直せませんでした", id))
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// handleDeleteTeamAgent は定義を消す。
//
// 有効にしている会話の名簿からは掃除しない。消えた定義を指している会話には
// 「定義が見つかりません」と名簿に出る。黙って人数が減るより、何が消えたかが
// 見えるほうがよい (#528664 の「開けるが続行できない」と同じ扱い)。
func (s *Server) handleDeleteTeamAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, ok := s.teamAgents.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound,
			fmt.Errorf("チームエージェント %q が見つかりません", id))
		return
	}
	if err := agent.Delete(a); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.reloadTeamAgents()
	w.WriteHeader(http.StatusNoContent)
}

// freeAgentID はその ID を使えるかを返す。
//
// 一意性は共通とチームの全体で保つ。ID はメンションの宛先であると同時に、
// 共有ディレクトリの中のファイル名でもある。2 つのチームエージェントが同じ
// 名前を持つことはファイル名として不可能で、共通と同じ名前を持てば、その
// 共通を有効にした会話で宛先が決まらなくなる。
func (s *Server) freeAgentID(id string) error {
	if err := agent.ValidateID(id); err != nil {
		return err
	}
	if _, ok := s.agents.Get(id); ok {
		return &agent.FieldError{Field: "id",
			Reason: fmt.Sprintf("ID %q は共通エージェントが使っています", id)}
	}
	if _, ok := s.teamAgents.Get(id); ok {
		return &agent.FieldError{Field: "id",
			Reason: fmt.Sprintf("ID %q は既にチームエージェントが使っています", id)}
	}
	return nil
}

// freeCopyID は写しに付ける既定の名前。
func (s *Server) freeCopyID(base string) string {
	for i := 2; i < 100; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if s.freeAgentID(id) == nil {
			return id
		}
	}
	return base + "-" + store.NewID()[:6]
}

func (s *Server) writeRoster(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := s.st.GetSession(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.rosterOf(sess))
}

// reloadTeamAgents は書いた内容を読み直す。書いたものを自分で読み返すことで、
// 画面が返す一覧とファイルの中身が食い違わない。
func (s *Server) reloadTeamAgents() { s.teamAgents.LoadTeam(s.cfg.TeamAgentPaths()) }

// canAnswer はその会話でそのエージェントを答え手にできるかを返す。
//
// チームでは答え手を選ばない。宛先はメンションで決まるので、選ばせる欄が
// あること自体が誤りになる (#640275)。
func (s *Server) canAnswer(r *http.Request, agentID string) error {
	sess, err := s.st.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if sess.Kind == config.KindTeam {
		return &agent.FieldError{Field: "agent_id",
			Reason: "チームセッションでは答え手を選びません。宛先はメンションで指定してください"}
	}
	if _, ok := s.agents.Get(agentID); !ok {
		return &agent.FieldError{Field: "agent_id",
			Reason: "エージェント " + agentID + " の定義が見つかりません"}
	}
	return nil
}
