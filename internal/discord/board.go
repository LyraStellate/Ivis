package discord

import (
	"context"
	"errors"
	"time"
)

const (
	// baseInterval は Discord へ反映する間隔。詰めるとレート制限に当たり、
	// 空けると生成が進んでいることが伝わらない。
	baseInterval = 1500 * time.Millisecond
	// maxInterval は断られ続けたときの上限。
	maxInterval = 8 * time.Second
	// calmAfter は元の間隔へ戻すまでに必要な連続成功の回数。
	calmAfter = 4
)

// board は 1 ターン分のメッセージ群を Discord 上に保つ。
//
// 出来事ごとにメッセージが増えていくので、あるべき並びを毎回受け取り、
// 足りないものを投稿し、変わったものを書き換え、要らなくなったものを消す。
// 送るのは一定の間隔でだけで、断られたら間隔を倍にする。設定に間隔を出さない
// のは、適切な値が環境ではなく Discord のレート制限で決まるためである。
type board struct {
	api       API
	channelID string
	replyTo   string

	interval time.Duration
	next     time.Time
	calm     int

	ids   map[string]string
	shown map[string]Payload
	// order は投稿した順の key。消すときに、並びを保ったまま辿るために持つ。
	order []string
}

func newBoard(api API, channelID, replyTo string) *board {
	return &board{api: api, channelID: channelID, replyTo: replyTo,
		interval: baseInterval, ids: map[string]string{}, shown: map[string]Payload{}}
}

// flush は必要なら Discord へ反映する。force が真なら間隔を待たない。
// ターンの終わりは待たずに最終形を出す。
func (b *board) flush(ctx context.Context, now time.Time, msgs []msg, force bool) error {
	if !force && now.Before(b.next) {
		return nil
	}

	want := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		want[m.key] = true
	}

	var err error
	// 要らなくなったものを先に消す。残したまま次を出すと、畳んだはずの
	// 推論の続きが下に残る。
	//
	// 消せなくても投稿は続ける。権限が足りずに消せないだけで回答が出なく
	// なるのは釣り合わない。次の回にまた試みる。
	for _, key := range append([]string{}, b.order...) {
		if want[key] {
			continue
		}
		if e := b.drop(ctx, key); e != nil {
			err = e
			if rateLimited(e) {
				b.pace(now, err)
				return err
			}
		}
	}

	for _, m := range msgs {
		// 投稿の失敗では止まる。飛ばして次を出すと、出来事の順が入れ替わる。
		if e := b.put(ctx, m); e != nil {
			err = e
			break
		}
	}

	b.pace(now, err)
	return err
}

// put は 1 通を、無ければ投稿し、あれば必要なときだけ書き換える。
func (b *board) put(ctx context.Context, m msg) error {
	if id, ok := b.ids[m.key]; ok {
		if same(b.shown[m.key], m.p) {
			return nil
		}
		if err := b.api.Edit(ctx, b.channelID, id, m.p); err != nil {
			return err
		}
		b.shown[m.key] = m.p
		return nil
	}

	reply := ""
	if m.reply {
		reply = b.replyTo
	}
	id, err := b.api.Send(ctx, b.channelID, reply, m.p)
	if err != nil {
		return err
	}
	b.ids[m.key] = id
	b.shown[m.key] = m.p
	b.order = append(b.order, m.key)
	return nil
}

func (b *board) drop(ctx context.Context, key string) error {
	id, ok := b.ids[key]
	if !ok {
		return nil
	}
	if err := b.api.Delete(ctx, b.channelID, id); err != nil {
		return err
	}
	delete(b.ids, key)
	delete(b.shown, key)
	for i, k := range b.order {
		if k == key {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
	return nil
}

func rateLimited(err error) bool {
	var rl *RateLimited
	return errors.As(err, &rl)
}

// pace は次に送ってよい時刻を決める。
func (b *board) pace(now time.Time, err error) {
	var rl *RateLimited
	if errors.As(err, &rl) {
		b.calm = 0
		if b.interval < maxInterval {
			b.interval *= 2
			if b.interval > maxInterval {
				b.interval = maxInterval
			}
		}
		wait := rl.RetryAfter
		if wait < b.interval {
			wait = b.interval
		}
		b.next = now.Add(wait)
		return
	}
	if err == nil {
		b.calm++
		if b.calm >= calmAfter && b.interval > baseInterval {
			b.interval /= 2
			if b.interval < baseInterval {
				b.interval = baseInterval
			}
			b.calm = 0
		}
	}
	b.next = now.Add(b.interval)
}

// same は出ている中身と送ろうとしている中身が同じかを返す。
func same(a, c Payload) bool {
	if a.Content != c.Content || len(a.Buttons) != len(c.Buttons) {
		return false
	}
	for i := range a.Buttons {
		if a.Buttons[i] != c.Buttons[i] {
			return false
		}
	}
	return true
}
