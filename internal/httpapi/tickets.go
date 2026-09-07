package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LyraStellate/Ivis/internal/store"
)

// チケットの経路 (#189542)。
//
// モデルが使うツールとは別に置く。画面から人が直せるようにするためで、
// 削除は人だけができる。モデルに消させないのは、消えたことが誰にも見えない
// からである — 注記はその消えた票に付いていた。

func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	f := store.TicketFilter{
		Assignee:      r.URL.Query().Get("assignee"),
		Status:        r.URL.Query().Get("status"),
		IncludeClosed: r.URL.Query().Get("closed") == "1",
	}
	list, err := s.st.ListTickets(r.Context(), r.PathValue("id"), f)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if list == nil {
		list = []*store.Ticket{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	n, err := ticketNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.st.GetTicket(r.Context(), r.PathValue("id"), n)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	sess, err := s.teamSession(r)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Assignee string `json:"assignee"`
		Due      string `json:"due"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t := &store.Ticket{SessionID: sess.ID, Title: in.Title, Body: in.Body,
		Assignee: in.Assignee, Due: in.Due, Status: in.Status, Priority: in.Priority}
	if err := s.st.CreateTicket(r.Context(), t); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handlePatchTicket は項目を変える。渡された項目だけを変えるので、指し手で
// 受ける。空文字も意思である — 担当を外す操作は、空文字を入れることでしか
// 表せない。
func (s *Server) handlePatchTicket(w http.ResponseWriter, r *http.Request) {
	n, err := ticketNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var in struct {
		Title    *string `json:"title"`
		Body     *string `json:"body"`
		Assignee *string `json:"assignee"`
		Due      *string `json:"due"`
		Status   *string `json:"status"`
		Priority *string `json:"priority"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.st.UpdateTicket(r.Context(), r.PathValue("id"), n, store.TicketPatch{
		Title: in.Title, Body: in.Body, Assignee: in.Assignee,
		Due: in.Due, Status: in.Status, Priority: in.Priority,
	}, "")
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleAddNote(w http.ResponseWriter, r *http.Request) {
	n, err := ticketNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	if _, err := s.st.GetTicket(r.Context(), id, n); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if err := s.st.AddNote(r.Context(), id, n, "", in.Body, false); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.st.GetTicket(r.Context(), id, n)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTicket(w http.ResponseWriter, r *http.Request) {
	n, err := ticketNumber(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteTicket(r.Context(), r.PathValue("id"), n); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func ticketNumber(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || n <= 0 {
		return 0, errors.New("チケットの番号が不正です")
	}
	return n, nil
}
