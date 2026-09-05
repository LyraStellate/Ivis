package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/store"
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
	sess, err := s.st.GetSession(r.Context(), id)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	// Discord の会話へは画面から送らせない。送っても内容はチャンネルに
	// 出ないので、次にそこで話す人は、自分の知らない文脈が挟まった状態で
	// 会話を続けることになる (#617204)。
	if sess.Source == store.SourceDiscord {
		writeError(w, http.StatusConflict,
			errors.New("この会話は Discord から進みます。画面からは送れません"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("ストリーミングに対応していません"))
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	if !s.runs.Begin(id, cancel) {
		cancel()
		writeError(w, http.StatusConflict, errors.New("このセッションは生成中です"))
		return
	}
	defer s.runs.End(id)

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
		// 実行ループが既に流した失敗は、ここでもう一度流さない。同じ文が
		// 2 度並ぶと、どちらを読めばよいか分からなくなる。
		if !engine.Reported(err) {
			send(engine.Event{Type: engine.EvtError, Error: err.Error(), Kind: kindOf(err)})
		}
	}
}

// handleCancel は生成中のターンを中断する。
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !s.runs.Cancel(r.PathValue("id")) {
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

// handleAnswer は問いへの答えを受ける。
func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Answer string `json:"answer"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	ch, ok := s.asking[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("その問いは既に終了しています"))
		return
	}
	select {
	case ch <- in.Answer:
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ask は engine.Asker の実装。答えが返るまで待ち、待機中も中断できる。
//
// 承認と同じく、どこで受けるかはその実行がどこから始まったかで決まる。
// 画面を開いていない相手に、画面でしか返せない問いを出しても答えは返らない。
func (s *Server) Ask(ctx context.Context, q engine.Question) (string, error) {
	if s.discord != nil && s.discord.Owns(q.SessionID) {
		return s.discord.Ask(ctx, q)
	}

	ch := make(chan string, 1)
	s.mu.Lock()
	s.asking[q.ID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.asking, q.ID)
		s.mu.Unlock()
	}()

	select {
	case ans := <-ch:
		return ans, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Request は engine.Approver の実装。承認が返るまで待ち、待機中も中断できる。
//
// 承認をどこで受けるかは、その実行がどこから始まったかで決まる。Discord から
// 始まった実行は Discord のボタンで受ける。画面を開いていない相手に、画面で
// しか返せない要求を出しても答えは返らない (#617204)。
func (s *Server) Request(ctx context.Context, req engine.ApprovalRequest) (bool, error) {
	if s.discord != nil && s.discord.Owns(req.SessionID) {
		return s.discord.Request(ctx, req)
	}

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
