package tools

import (
	"context"
	"sync/atomic"
	"time"
)

// 実行に時間の上限は置かない (#470913)。
//
// 時間で切ると、正しく進んでいる長い仕事まで止まる。npm install も go build も
// 当たり前に数分かかり、そこに上限を置けば、上限を超えた瞬間に「途中まで入った
// ライブラリ」が残る。止めるべきなのは進んでいないときだけである。
//
// 同じ結論に 2 度到達している。ツールの往復の上限は回数から「直前と同じ呼び出しが
// 続いた回数」へ変わり (#470913)、提供元への接続は総時間から「何も届かない時間」へ
// 変わった (#903215)。ここはその 3 つ目である。
//
// 見張るのは「動いていない時間」で、それは次の 2 つで数え直す。
//
//   - 出力が届いた
//   - 木全体の仕事量 (CPU 時間・I/O) が進んだ
//
// 出力だけで判じると、無言で数分走るビルドを殺す。仕事量だけで判じると、
// 仕事量を測れない環境で見張りそのものが効かなくなる。

// watch は、動かないまま待ち続けないための見張り。
//
// 数え直されないまま上限を過ぎたら、実行ごと取り消す。取り消しは利用者による
// 中断と同じ形で伝わるので、どちらだったかを expired で見分けられるようにして
// おく — 黙って終わったように見せると、待っていた側は「終わったのか、止まった
// のか」が分からない。
type watch struct {
	idle    time.Duration
	timer   *time.Timer
	timedUp atomic.Bool
	// blind は木の仕事量を測れなかったこと。測れないときは出力だけで判じる
	// ので、無言で働くものを切りうる。切ったときにそう言えるように覚える。
	blind atomic.Bool
}

// newWatch は見張りを始める。idle が 0 以下なら見張らない。
func newWatch(idle time.Duration, cancel context.CancelFunc) *watch {
	w := &watch{idle: idle}
	if idle <= 0 {
		return w
	}
	w.timer = time.AfterFunc(idle, func() {
		w.timedUp.Store(true)
		cancel()
	})
	return w
}

// seen は「いま動いた」を伝え、上限を数え直す。
func (w *watch) seen() {
	if w.timer != nil {
		w.timer.Reset(w.idle)
	}
}

func (w *watch) stop() {
	if w.timer != nil {
		w.timer.Stop()
	}
}

// expired は見張りが打ち切ったかどうか。利用者の中断と区別するために要る。
func (w *watch) expired() bool { return w.timedUp.Load() }

// workInterval は木の仕事量を見に行く間隔。
//
// 短くしても、見張りの上限は秒単位なので何も得られない。数え直しが 1 秒ぶん
// 遅れて困る長さの上限は、そもそも短すぎる。
const workInterval = time.Second

// pollWork は木の仕事量を定期的に見て、進んでいれば見張りを数え直す。
//
// これが無いと、出力を出さないコマンド (無言で走るビルド、圧縮、テスト) が
// 「止まっている」と判じられる。
//
// 測れない環境 (work が ok=false を返す) では何もしない。測れないことを黙って
// 「動いている」ことにはしない — それでは見張りが無いのと同じになる。
func pollWork(ctx context.Context, t tracker, w *watch) {
	if t == nil {
		w.blind.Store(true)
		return
	}
	last, ok := t.work()
	if !ok {
		w.blind.Store(true)
		return
	}
	tick := time.NewTicker(workInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			n, ok := t.work()
			if !ok {
				w.blind.Store(true)
				return
			}
			if n != last {
				last = n
				w.seen()
			}
		}
	}
}
