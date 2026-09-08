package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/LyraStellate/Ivis/internal/command"
	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
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
	// 出ないので、次にそこで話す人は、自分の知らないやり取りが挟まった状態で
	// 会話を続けることになる (#617204)。
	if sess.Source == store.SourceDiscord {
		writeError(w, http.StatusConflict,
			errors.New("この会話は Discord から進みます。画面からは送れません"))
		return
	}

	if _, ok := w.(http.Flusher); !ok {
		writeError(w, http.StatusInternalServerError, errWriterCannotStream)
		return
	}

	// コマンドかどうかを、占有を取る前に見る。占有を取ってからだと /stop が
	// 「生成中です」で断られ、止めたいときに止められない (#486237)。
	if name, arg, ok := command.Parse(in.Text); ok {
		s.runCommand(w, r, sess, name, arg)
		return
	}

	// 実行はこの要求の寿命から切り離す。要求に紐づけると、ブラウザを更新した
	// 瞬間に走っていた生成ごと止まる。書きかけのファイルも、承認待ちの手番も
	// そこで捨てられる。止めるのは利用者が止めたときだけにする。
	ctx, cancel := context.WithCancel(context.Background())
	if !s.runs.Begin(id, cancel) {
		cancel()
		writeError(w, http.StatusConflict, errors.New("このセッションは生成中です"))
		return
	}

	lv := s.beginLive(id)
	go func() {
		defer s.runs.End(id)
		defer lv.close()

		if err := s.eng.Run(ctx, id, in.Text, lv.emit); err != nil {
			if ctx.Err() != nil {
				// 利用者による中断。部分出力は保存済みなので、その旨だけ伝える。
				lv.emit(engine.Event{Type: engine.EvtDone, Text: "中断しました"})
				return
			}
			// 実行ループが既に流した失敗は、ここでもう一度流さない。同じ文が
			// 2 度並ぶと、どちらを読めばよいか分からなくなる。
			if !engine.Reported(err) {
				lv.emit(engine.Event{Type: engine.EvtError, Error: err.Error(), Kind: kindOf(err)})
			}
		}
	}()

	s.follow(w, r, lv)
}

// errWriterCannotStream は応答が少しずつ書けないこと。
var errWriterCannotStream = errors.New("ストリーミングに対応していません")

// runCommand はコマンドを実行し、その返事を生成と同じ経路で流す。
//
// 画面から見て送信の応答は常に同じ形にする。コマンドだけ別の返し方にすると、
// 送った側は打ち終えるまでどちらが返るか分からない。
//
// 先に流し口を開けてから実行するのは、圧縮のように数十秒かかるコマンドが
// あるためである。終わってから開けると、その間は何も届かない。
func (s *Server) runCommand(w http.ResponseWriter, r *http.Request, sess *store.Session, name, arg string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("ストリーミングに対応していません"))
		return
	}
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

	deps := &command.Deps{
		Store: s.st,
		Runs:  s.runs,
		Compact: func(ctx context.Context, id, instructions string) (*engine.CompactResult, error) {
			// 何をしているかを、待っている間に出す。要約はまとめて 1 度に
			// 届くので、こちらで報告しないと無音のままになる。
			send(engine.Event{Type: engine.EvtStage, Stage: engine.StageCompacting})
			defer send(engine.Event{Type: engine.EvtStage})
			return s.eng.Compact(ctx, id, instructions, func(chars int) {
				send(engine.Event{Type: engine.EvtStage, Stage: engine.StageCompacting, Done: chars})
			})
		},
	}
	send(engine.Event{Type: engine.EvtNotice,
		Text: command.Run(r.Context(), deps, sess, name, arg)})

	// /clear はチケットも消す。伝えないと、消えた一覧が出たままになる。
	send(engine.Event{Type: engine.EvtChanged, Text: tools.ChangedTickets})

	// 履歴を消す・まとめるコマンドは使用量を変える。伝えないと、既に空いて
	// いるのに満杯のままの割合が出続ける。
	if sess != nil {
		if cur, err := s.st.GetSession(r.Context(), sess.ID); err == nil {
			send(engine.Event{Type: engine.EvtUsage,
				PromptTokens: cur.ContextTokens, ContextLimit: cur.ContextLimit})
		}
	}
	send(engine.Event{Type: engine.EvtDone})
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
