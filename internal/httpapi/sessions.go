package httpapi

import (
	"errors"
	"net/http"
	"os"

	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/store"
)

// sessionBody は会話 1 件の応答。作業ディレクトリは ID から一意に決まる
// 派生物なので保存せず、返すときに組み立てる。保存すると、設定の作業
// ディレクトリを変えたときに食い違う。
type sessionBody struct {
	*store.Session
	Workspace string `json:"workspace"`
}

func (s *Server) body(sess *store.Session) sessionBody {
	return sessionBody{Session: sess, Workspace: s.cfg.SessionWorkspace(sess.Kind, sess.ID)}
}

func (s *Server) bodies(list []*store.Session) []sessionBody {
	out := make([]sessionBody, 0, len(list))
	for _, sess := range list {
		out = append(out, s.body(sess))
	}
	return out
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.bodies(list))
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID string `json:"agent_id"`
		Title   string `json:"title"`
		// Kind は会話の進み方。省略すれば直列で、既存の呼び出しは変わらない。
		Kind string `json:"kind"`
	}
	if err := decodeJSON(r, &in); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if in.AgentID == "" {
		in.AgentID = s.cfg.DefaultAgent
	}
	if _, ok := s.agents.Get(in.AgentID); !ok {
		// 存在しないエージェントでセッションを作らせない。作れてしまうと、
		// 最初の送信ではじめて失敗する。
		writeError(w, http.StatusBadRequest,
			errors.New("エージェント "+in.AgentID+" の定義が見つかりません"))
		return
	}
	create := s.st.CreateSession
	if in.Kind == config.KindTeam {
		create = s.st.CreateTeamSession
	}
	sess, err := create(r.Context(), in.AgentID, in.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// 作業場所は会話を作った時点で用意する。利用者が材料を先に置けるように
	// するため。作れなくても会話自体は使えるので、ここでは止めない。
	_ = os.MkdirAll(s.cfg.SessionWorkspace(sess.Kind, sess.ID), 0o755)
	writeJSON(w, http.StatusOK, s.body(sess))
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.st.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.body(sess))
}

func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title   string `json:"title"`
		AgentID string `json:"agent_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if in.AgentID != "" {
		if err := s.canAnswer(r, in.AgentID); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
	}
	if err := s.st.UpdateSession(r.Context(), r.PathValue("id"), in.Title, in.AgentID); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	sess, err := s.st.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.body(sess))
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.runs.Cancel(id)
	// その会話が起動したものも一緒に止める。会話を消したのに、その会話が
	// 走らせたものだけが残る状態を作らない。
	s.procs.CloseSession(id)
	if err := s.st.DeleteSession(r.Context(), id); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	// 固有のエージェントの定義も消す。その会話のために作られたもので、
	// 会話が無くなれば読み手が居ない (#731906)。
	s.removeSessionAgents(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if list == nil {
		list = []*store.Message{}
	}
	writeJSON(w, http.StatusOK, list)
}

// rewindBody は巻き戻しの結果。件数と本文を返すのは、画面が「何件消えたか」
// を確認に出し、消した依頼をそのまま入力欄へ戻すためである。
type rewindBody struct {
	Deleted int `json:"deleted"`
	// Tickets はその地点より後に起票され、一緒に消えたチケットの数。
	Tickets int         `json:"tickets"`
	Text    string      `json:"text"`
	Session sessionBody `json:"session"`
}

// handleRewind は指定した利用者の発言と、それ以降の全てを消す。
//
// 作業ディレクトリには触らない。会話は戻せてもファイルは戻せないため、
// 戻したように見せない。
func (s *Server) handleRewind(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		MessageID string `json:"message_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// 生成中に履歴を消すと、走っているターンが自分の書き込み先を失う。
	if s.runs.Running(id) {
		writeError(w, http.StatusConflict, errors.New("生成中は巻き戻せません"))
		return
	}

	got, text, err := s.st.Rewind(r.Context(), id, in.MessageID)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	sess, err := s.st.GetSession(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, rewindBody{Deleted: got.Messages, Tickets: got.Tickets,
		Text: text, Session: s.body(sess)})
}
