// Package discord は Discord と実行エンジンを繋ぐ (#617204)。
//
// メンションを受けたら対応する会話で 1 ターン走らせ、その経過を 1 通の
// リプライメッセージへ描き続ける。描画は外部と一切やり取りしない純粋な
// 部分に切り出してあり (render.go)、送信の間引きもそこから分けてある。
// エンジンのイベントは生成ループから同期的に届くので、そこで HTTP を
// 投げると送信の待ち時間だけ生成が止まる。
package discord

import (
	"context"
	"time"
)

// Payload は 1 通のメッセージの中身。
//
// 枠 (埋め込み) は持たない。経過も回答も同じ 1 通に収め、書式だけで
// 区別する (#617204)。
type Payload struct {
	Content string
	// Buttons は承認を求めるときだけ付く。
	Buttons []Button
}

// Button は承認の応答を受ける押しボタン。
type Button struct {
	ID    string
	Label string
	// Danger が真なら目立たない側 (やめる) として描く。
	Danger bool
}

// API は Discord へ実際に届ける口。試験では差し替える。
//
// ライブラリの型をここから先へ持ち込まないのは、描画と間引きを Discord
// 無しで試せる状態に保つためである。
type API interface {
	// Send は新しいメッセージを投稿し、その識別子を返す。replyTo が空でなければ
	// そのメッセージへのリプライとして投稿する。
	Send(ctx context.Context, channelID, replyTo string, p Payload) (string, error)
	// Edit は投稿済みのメッセージを書き換える。
	Edit(ctx context.Context, channelID, messageID string, p Payload) error
	// Delete は投稿済みのメッセージを消す。伸ばした推論を畳むと要らなく
	// なる分が出るので、消す手段が要る。
	Delete(ctx context.Context, channelID, messageID string) error
	// Typing は「入力中」を出す。約 10 秒で消えるので、続けるには送り直す。
	Typing(ctx context.Context, channelID string) error
	// ChannelName は表題に使う名前を返す。取れなければ空。
	ChannelName(ctx context.Context, channelID string) string
}

// RateLimited は送りすぎを断られたこと。待つべき時間を伴う。
type RateLimited struct {
	RetryAfter time.Duration
}

func (e *RateLimited) Error() string {
	return "Discord に送りすぎました。" + e.RetryAfter.String() + " 待ちます"
}
