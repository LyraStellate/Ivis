package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/LyraStellate/Ivis/internal/engine"
)

// 実行と、それを見ている画面を切り離す。
//
// もとは 1 回の実行が送信の HTTP リクエストそのものに乗っていた。経過を返す
// 経路と実行の寿命が同じものだったので、ブラウザを更新した瞬間に r.Context()
// が切れ、走っていた生成ごと止まっていた。書きかけのファイルも、承認待ちの
// 手番も、そこで捨てられる。
//
// 実行は自分の寿命で走らせ、経過はここへ流す。画面はいつでも繋ぎ直せる。
// 見ている者が居なくなっても実行は続き、戻ってくればその続きから見える。

// maxBuffered は 1 回の実行で覚えておく経過の数。繋ぎ直したときに、それまでの
// 経過を流し直すために持つ。
//
// 上限を置くのは、長い生成では delta が数万件になるためである。溢れたら
// 中身のある出来事 (発言の始まり、道具の呼び出し) を残して字だけを捨てる。
// 捨てた分は画面の側で埋まる — 実行が終わったところで履歴を読み直すので、
// 正しいのは常にデータベースのほうである。
const maxBuffered = 20000

// live は 1 回の実行の中継。
type live struct {
	mu      sync.Mutex
	buf     []engine.Event
	trimmed bool
	subs    map[chan engine.Event]struct{}
	done    bool
}

func newLive() *live {
	return &live{subs: map[chan engine.Event]struct{}{}}
}

// emit は経過を 1 件、覚えたうえで見ている者へ配る。
func (l *live) emit(ev engine.Event) {
	l.mu.Lock()
	l.buf = append(l.buf, ev)
	if len(l.buf) > maxBuffered {
		l.trim()
	}
	subs := make([]chan engine.Event, 0, len(l.subs))
	for ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	for _, ch := range subs {
		// 読み手が詰まっていても実行は止めない。取りこぼした分は、実行が
		// 終わったところで履歴を読み直せば埋まる。
		select {
		case ch <- ev:
		default:
		}
	}
}

// trim は溜まりすぎた経過を削る。呼ぶ側が mu を持っていること。
func (l *live) trim() {
	kept := l.buf[:0]
	for _, ev := range l.buf {
		if ev.Type == engine.EvtDelta || ev.Type == engine.EvtThinking {
			continue
		}
		kept = append(kept, ev)
	}
	l.buf = kept
	l.trimmed = true
	// 字を捨ててもまだ多いなら、古い側から落とす。
	if len(l.buf) > maxBuffered {
		l.buf = l.buf[len(l.buf)-maxBuffered:]
	}
}

// close は実行が終わったことを伝える。
func (l *live) close() {
	l.mu.Lock()
	l.done = true
	subs := make([]chan engine.Event, 0, len(l.subs))
	for ch := range l.subs {
		subs = append(subs, ch)
	}
	l.subs = map[chan engine.Event]struct{}{}
	l.mu.Unlock()

	for _, ch := range subs {
		close(ch)
	}
}

// join は、それまでの経過と、以後の経過が流れてくる口を返す。
// 実行が既に終わっていれば口は nil になる。
func (l *live) join() ([]engine.Event, chan engine.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()

	replay := append([]engine.Event{}, l.buf...)
	if l.done {
		return replay, nil
	}
	// 詰まっても実行を止めないよう、少し余裕を持たせる。
	ch := make(chan engine.Event, 256)
	l.subs[ch] = struct{}{}
	return replay, ch
}

func (l *live) leave(ch chan engine.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.subs[ch]; ok {
		delete(l.subs, ch)
		close(ch)
	}
}

func (l *live) finished() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.done
}

// beginLive はその会話の中継を新しく作る。前の実行の分は捨てる。
func (s *Server) beginLive(id string) *live {
	lv := newLive()
	s.liveMu.Lock()
	s.lives[id] = lv
	s.liveMu.Unlock()
	return lv
}

func (s *Server) liveOf(id string) *live {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return s.lives[id]
}

// follow は 1 つの中継を SSE で流す。
//
// 途中で読み手が居なくなっても、実行は止めない。ここで返るのは「この画面は
// もう見ていない」というだけのことである。
func (s *Server) follow(w http.ResponseWriter, r *http.Request, lv *live) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errWriterCannotStream)
		return
	}
	replay, ch := lv.join()
	defer func() {
		if ch != nil {
			lv.leave(ch)
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	write := func(ev engine.Event) bool {
		b, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for _, ev := range replay {
		if !write(ev) {
			return
		}
	}
	if ch == nil {
		return
	}

	for {
		select {
		case ev, open := <-ch:
			if !open {
				return
			}
			if !write(ev) {
				return
			}
		case <-r.Context().Done():
			// 画面が離れただけ。実行はそのまま続く。
			return
		}
	}
}

// handleAttach は走っている実行へ繋ぎ直す。
//
// 走っていなければ 204 を返す。画面はそれを見て、履歴を読み直すだけに切り替える。
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lv := s.liveOf(id)
	if lv == nil || lv.finished() || !s.runs.Running(id) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.follow(w, r, lv)
}
