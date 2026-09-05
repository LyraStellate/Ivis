package discord

import (
	"context"
	"sync"
	"time"

	"github.com/LyraStellate/Ivis/internal/engine"
)

// tickInterval は送信を試みる周期。実際に送るかどうかは sender が間隔を見て
// 決めるので、ここは刻みの細かさでしかない。
const tickInterval = 300 * time.Millisecond

// run は走っている 1 ターン。イベントを受ける側と送る側が別の goroutine に
// なるため、状態はここで守る。
//
// エンジンのイベントは生成ループから同期的に届く。受け取ったその場で
// Discord へ投げると、送信の待ち時間だけ生成が止まる (#617204)。
type run struct {
	mu   sync.Mutex
	turn *turn
	brd  *board
	now  func() time.Time

	// wake は間隔を待たずに送りたいときの合図。承認のように、遅れると
	// 人を待たせるものに使う。
	wake chan struct{}
}

func newRun(api API, channelID, replyTo string,
	nameOf func(string) string, now func() time.Time) *run {
	return &run{
		turn: newTurn(nameOf),
		brd:  newBoard(api, channelID, replyTo),
		now:  now,
		wake: make(chan struct{}, 1),
	}
}

// emit はエンジンからのイベントを取り込む。
func (r *run) emit(ev engine.Event) {
	r.mu.Lock()
	r.turn.apply(ev, r.now())
	r.mu.Unlock()
}

// nudge は次の周期を待たずに送るよう促す。
func (r *run) nudge() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// waiting は人の応答を待っているかを返す。待っている間はボットは動いて
// いないので、「入力中」を出し続けるのは嘘になる。
func (r *run) waiting() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, it := range r.turn.items {
		if it.waiting {
			return true
		}
	}
	return false
}

// answered は待ちの印を外す。押されたのに押しボタンが残っていると、もう一度
// 押せるように見える。問いも同じで、答えたのに問われたままに見えては困る。
func (r *run) answered(id string) {
	r.mu.Lock()
	for _, it := range r.turn.items {
		if it.approvalID == id || it.questionID == id {
			it.waiting = false
			it.approvalID = ""
			it.questionID = ""
		}
	}
	r.mu.Unlock()
	r.nudge()
}

// finish はターンの終わりを記録する。
func (r *run) finish(stopped bool) {
	r.mu.Lock()
	r.turn.finish(r.now(), stopped)
	r.mu.Unlock()
}

// flush は現在の状態を送る。
func (r *run) flush(ctx context.Context, force bool) error {
	r.mu.Lock()
	v := r.turn.view(r.now())
	r.mu.Unlock()
	return r.brd.flush(ctx, r.now(), v, force)
}

// pump は終わるまで一定の周期で送り続ける。
func (r *run) pump(ctx context.Context, done <-chan struct{}, onErr func(error)) {
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-r.wake:
			if err := r.flush(ctx, true); err != nil {
				onErr(err)
			}
		case <-tick.C:
			if err := r.flush(ctx, false); err != nil {
				onErr(err)
			}
		}
	}
}
