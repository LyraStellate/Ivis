package discord

import (
	"context"
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/engine"
)

// approveWait は押されないまま置かれた承認を諦めるまでの時間。
//
// 諦めたら拒否として扱う。エンジンは拒否をモデルへ返して続きを進められる
// ので、ターン全体が失敗するわけではない。定数にしないのは、期限が来たとき
// の振る舞いを試験で確かめるためである。
var approveWait = 5 * time.Minute

// 押しボタンの識別子。頭を付けるのは、他のボットのボタンと取り違えない
// ようにするためである。
const (
	btnPrefix = "ivis:"
	btnOK     = "ok"
	btnNo     = "no"
)

func approveID(id string, ok bool) string {
	if ok {
		return btnPrefix + btnOK + ":" + id
	}
	return btnPrefix + btnNo + ":" + id
}

// parseButton は押されたボタンの識別子を読む。自分のものでなければ false。
func parseButton(customID string) (kind, arg string, ok bool) {
	if !strings.HasPrefix(customID, btnPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(customID, btnPrefix)
	i := strings.Index(rest, ":")
	if i <= 0 {
		return "", "", false
	}
	kind, arg = rest[:i], rest[i+1:]
	switch kind {
	case btnOK, btnNo:
		return kind, arg, arg != ""
	}
	return "", "", false
}

// pending は押されるのを待っている承認 1 件。
type pending struct {
	ch chan bool
	// asker は押せる相手。呼びかけた本人だけが押せる。他の人が押せると、
	// 依頼していない人が実行を通せることになる。
	asker string
}

// Owns はその会話をいま Discord 側が動かしているかを返す。
func (b *Bridge) Owns(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.active[sessionID]
	return ok
}

// Request は engine.Approver の実装。ツールの埋め込みへボタンを出し、
// 押されるか期限が切れるまで待つ。
func (b *Bridge) Request(ctx context.Context, req engine.ApprovalRequest) (bool, error) {
	b.mu.Lock()
	r := b.active[req.SessionID]
	asker := b.asker[req.SessionID]
	b.mu.Unlock()
	if r == nil {
		// 引き受けたはずの会話が消えている。実行しないほうを選ぶ。
		return false, nil
	}

	ch := make(chan bool, 1)
	b.mu.Lock()
	b.waits[req.ID] = &pending{ch: ch, asker: asker}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waits, req.ID)
		b.mu.Unlock()
		// 押された時点でボタンを消す。押せるように見えて何も起きない
		// ボタンを残さない。
		r.answered(req.ID)
	}()

	// 待たせる側なので、間隔を待たずに出す。
	r.nudge()

	timer := time.NewTimer(approveWait)
	defer timer.Stop()

	select {
	case ok := <-ch:
		return ok, nil
	case <-timer.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// resolve は押されたボタンを承認の待ち合わせへ届ける。押せる相手でなければ
// 断り、その旨を押した人にだけ返す。
func (b *Bridge) resolve(id string, approved bool, presser string) (accepted bool, known bool) {
	b.mu.Lock()
	p, ok := b.waits[id]
	b.mu.Unlock()
	if !ok {
		return false, false
	}
	if p.asker != "" && presser != p.asker {
		return false, true
	}
	select {
	case p.ch <- approved:
	default:
	}
	return true, true
}

// StopCommand は生成を打ち切るスラッシュコマンドの名前。
const StopCommand = "stop"

// stop はそのチャンネルで走っているターンを打ち切る。
//
// 使える相手を絞らないのは、止めることが誰にとっても安全だからである。
// 承認は実行を通す操作なので本人に限るが、暴走を止める操作までその場に
// 居合わせた人ができないと、緊急停止の役に立たない。
func (b *Bridge) stop(ctx context.Context, channelID string) bool {
	sess, err := b.deps.Store.SessionByChannel(ctx, channelID)
	if err != nil {
		return false
	}
	if !b.Owns(sess.ID) {
		return false
	}
	return b.deps.Runs.Cancel(sess.ID)
}
