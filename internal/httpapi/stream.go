package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/LyraStellate/Ivis/internal/engine"
)

// handleSend は 1 ターンを実行し、経過を SSE で流す。
//
// 通信は一方向で足りるため WebSocket ではなく SSE を使う。承認のように
// 逆向きが必要なやり取りだけ、別のエンドポイントで受ける。
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if in.Text == "" {
		writeError(w, http.StatusBadRequest, errors.New("本文が空です"))
		return
	}
	if _, err := s.st.GetSession(r.Context(), id); err != nil {
		writeError(w, statusFor(err), err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("ストリーミングに対応していません"))
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	if !s.beginRun(id, cancel) {
		cancel()
		writeError(w, http.StatusConflict, errors.New("このセッションは生成中です"))
		return
	}
	defer s.endRun(id)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	send := func(ev engine.Event) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	if err := s.eng.Run(ctx, id, in.Text, send); err != nil {
		if ctx.Err() != nil {
			// 利用者による中断。部分出力は保存済みなので、その旨だけ伝える。
			send(engine.Event{Type: engine.EvtDone, Text: "中断しました"})
			return
		}
		send(engine.Event{Type: engine.EvtError, Error: err.Error()})
	}
}

// handleCancel は生成中のターンを中断する。
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !s.cancelRun(r.PathValue("id")) {
		writeError(w, http.StatusConflict, errors.New("このセッションは生成中ではありません"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleApproval は承認要求への応答を受ける。
func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Approved bool `json:"approved"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	ch, ok := s.pending[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("その承認要求は既に終了しています"))
		return
	}
	select {
	case ch <- in.Approved:
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

// Request は engine.Approver の実装。承認が返るまで待ち、待機中も中断できる。
func (s *Server) Request(ctx context.Context, req engine.ApprovalRequest) (bool, error) {
	ch := make(chan bool, 1)
	s.mu.Lock()
	s.pending[req.ID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()

	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *Server) beginRun(id string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.running[id]; busy {
		return false
	}
	s.running[id] = cancel
	return true
}

func (s *Server) endRun(id string) {
	s.mu.Lock()
	cancel, ok := s.running[id]
	delete(s.running, id)
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func (s *Server) cancelRun(id string) bool {
	s.mu.Lock()
	cancel, ok := s.running[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
