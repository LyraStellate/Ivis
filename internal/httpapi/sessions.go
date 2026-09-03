package httpapi

import (
	"errors"
	"net/http"

	"github.com/LyraStellate/Ivis/internal/store"
)

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if list == nil {
		list = []*store.Session{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID string `json:"agent_id"`
		Title   string `json:"title"`
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
	sess, err := s.st.CreateSession(r.Context(), in.AgentID, in.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.st.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
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
		if _, ok := s.agents.Get(in.AgentID); !ok {
			writeError(w, http.StatusBadRequest,
				errors.New("エージェント "+in.AgentID+" の定義が見つかりません"))
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
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.cancelRun(id)
	if err := s.st.DeleteSession(r.Context(), id); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
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
