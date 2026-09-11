package tools

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// 実行中の出力を、溜めながら画面へも流す (#470913)。
//
// これまでは bytes.Buffer に溜めるだけで、終わるまで誰も中身を見られなかった。
// 5 分走るビルドは、5 分間なにも起きていないように見える。推論を流すのと同じ
// 形で、届いたそばから流す。
//
// 流すのは画面のためだけで、モデルへ渡すものは変わらない。最終的な結果は
// 今までどおり全部を (上限で切って) 返す。

// tap は書き込みを覗きながら、そのまま下へ渡す。
//
// 見張りを数え直すのも、画面へ流すのも、ここを通る。届いたことを知る場所は
// 1 つでよく、2 つに分けると片方だけが更新される形をいずれ作る。
type tap struct {
	to   io.Writer
	seen func()
	live *liveOut
}

func (t *tap) Write(p []byte) (int, error) {
	n, err := t.to.Write(p)
	if n > 0 {
		if t.seen != nil {
			t.seen()
		}
		if t.live != nil {
			t.live.add(p[:n])
		}
	}
	return n, err
}

// liveFlush は画面へ流す間隔。
//
// 1 片ごとに流すと、経路がログだけで埋まる。まとめて送る — 目で追える速さは
// もともとこのあたりが限度である。要約の進み具合を 120 バイトごとにまとめて
// いるのと同じ考え方 (#486237)。
const liveFlush = 100 * time.Millisecond

// liveHold は改行が来ないまま溜めておく上限。
//
// 進捗表示のように改行を出さないものがあるので、待ち続けない。
const liveHold = 8 << 10

// liveOut は溜まった出力を、間隔を置いてまとめて流す。
type liveOut struct {
	out func(string)

	mu   sync.Mutex
	buf  []byte
	stop chan struct{}
	done chan struct{}
}

func newLiveOut(out func(string)) *liveOut {
	l := &liveOut{out: out}
	if out == nil {
		return l
	}
	l.stop = make(chan struct{})
	l.done = make(chan struct{})
	go l.loop()
	return l
}

func (l *liveOut) add(p []byte) {
	if l.out == nil {
		return
	}
	l.mu.Lock()
	l.buf = append(l.buf, p...)
	l.mu.Unlock()
}

func (l *liveOut) loop() {
	defer close(l.done)
	tick := time.NewTicker(liveFlush)
	defer tick.Stop()
	for {
		select {
		case <-l.stop:
			l.flush(true)
			return
		case <-tick.C:
			l.flush(false)
		}
	}
}

// flush は溜まった分を流す。last なら残り全部を出す。
//
// 文字にするのは行の切れ目で行う。届いた片ごとに変換すると、多バイト文字の
// 途中で切れた片を化けたものとして確定させてしまう。改行は 1 バイトなので、
// そこで区切れば途中で切ることはない。
func (l *liveOut) flush(last bool) {
	l.mu.Lock()
	if len(l.buf) == 0 {
		l.mu.Unlock()
		return
	}
	cut := bytes.LastIndexByte(l.buf, '\n') + 1
	if last || (cut == 0 && len(l.buf) >= liveHold) {
		cut = len(l.buf)
	}
	if cut == 0 {
		l.mu.Unlock()
		return
	}
	chunk := l.buf[:cut]
	l.buf = append([]byte(nil), l.buf[cut:]...)
	l.mu.Unlock()

	if s := decodeConsole(chunk); s != "" {
		l.out(s)
	}
}

func (l *liveOut) close() {
	if l.out == nil || l.stop == nil {
		return
	}
	close(l.stop)
	<-l.done
	l.stop = nil
}

// stalled は、動かないまま上限を過ぎたことを伝える文。
//
// 何がどれだけ動かなかったのかと、次に取れる手を書く。設定画面のラベルを
// そのまま名指しするのは、直す場所を探させないためである。
func stalled(what string, idle time.Duration, label string, blind bool) string {
	var b strings.Builder
	if blind {
		// 仕事量を測れなかった。無言で働くものも切ってしまうので、その旨を
		// 言う — 言わないと、利用者は動いていたものが切られた理由を探せない。
		fmt.Fprintf(&b, "(%sが %s のあいだ何も出力しなかったので打ち切りました。", what, idle)
		b.WriteString("この環境では処理の進み具合を測れないため、出力だけで判じています。")
	} else {
		fmt.Fprintf(&b, "(%sが %s のあいだ、出力も処理も進まなかったので打ち切りました。", what, idle)
		b.WriteString("入力待ちで止まっているか、応答の無い相手を待っている可能性があります。")
	}
	fmt.Fprintf(&b, "時間のかかる処理なら、設定の「%s」を延ばしてください)", label)
	return b.String()
}
