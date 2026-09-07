package engine

import (
	"context"
	"sync"
)

// Runs は会話ごとの実行の占有。
//
// 1 つの会話で 2 つのターンが同時に走ると、履歴の書き込み先が衝突する。
// 入口が Web だけだった間はこれを HTTP 層が持っていたが、Discord という
// 2 つ目の入口ができたので、両方から見える場所へ移した (#617204)。
// どちらか一方だけが知る占有では、Web で送信中の会話へ Discord から
// メンションが来たとき (逆も同じく) に二重に走る。
type Runs struct {
	mu sync.Mutex
	// m は走っている会話の中断関数。存在すること自体が占有の印になる。
	m map[string]context.CancelFunc
}

// NewRuns は空の占有を返す。
func NewRuns() *Runs { return &Runs{m: map[string]context.CancelFunc{}} }

// Begin は占有を試みる。既に走っていれば false を返す。
func (r *Runs) Begin(id string, cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.m[id]; busy {
		return false
	}
	r.m[id] = cancel
	return true
}

// End は占有を解き、中断関数を呼ぶ。呼ぶのは、途中で抜けた場合に中断が
// 呼ばれないまま残るのを避けるためである。
func (r *Runs) End(id string) {
	r.mu.Lock()
	cancel, ok := r.m[id]
	delete(r.m, id)
	r.mu.Unlock()
	if ok {
		cancel()
	}
}

// Running はその会話で生成が走っているかを返す。
func (r *Runs) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, busy := r.m[id]
	return busy
}

// Cancel は走っているターンを中断する。走っていなければ false。
// 占有は解かない。解くのは走っている側の仕事で、ここで消すと後から
// 始まったターンの中断関数を取り違える。
func (r *Runs) Cancel(id string) bool {
	r.mu.Lock()
	cancel, ok := r.m[id]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
