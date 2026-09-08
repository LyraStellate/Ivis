// Package command は、本文の先頭が / のときに実行する指示をまとめる。
//
// コマンドは、呼びかけの本文の先頭が / かどうかで見分ける。
//
// Discord のスラッシュコマンドとして登録しない。登録には招待に
// applications.commands が要り、既に招待済みのボットでは入れ直しになる。
// サーバーごとの登録は反映が遅れることもあり、入れた直後に止められないので
// あれば緊急停止の役に立たない (#617204)。
//
// 表を Discord から出してここへ置いたのは、同じ字面が入口によって別の意味に
// なるのを避けるためである。/compact は Web からも打てる必要があり、両方に
// 表を持つと、片方だけ増えた状態が必ず生まれる (#486237)。
package command

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/store"
)

// Deps はコマンドの実行に要るもの。
//
// 圧縮を関数で受け取るのは、この表がエンジンの実体に依存しないようにする
// ためである。入口はどちらもエンジンを持っているが、ここが持つ必要はない。
type Deps struct {
	Store *store.Store
	Runs  *engine.Runs
	// Compact は 1 つの会話のコンテキストを圧縮する。engine.Engine.Compact。
	Compact func(ctx context.Context, sessionID, instructions string) (*engine.CompactResult, error)
}

// Parse は本文からコマンド名と、その後ろを返す。
//
// 名前は英字だけとし、大文字小文字は問わない。/Stop と /stop を別のものに
// すると、打った本人にはどちらを打ったか見えているので、効かない理由が
// 分からない。
func Parse(text string) (name, rest string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	body := text[1:]
	end := len(body)
	for i, r := range body {
		if !unicode.IsLetter(r) {
			end = i
			break
		}
	}
	name = strings.ToLower(body[:end])
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(body[end:]), true
}

// Command は 1 つのコマンド。返した文がそのまま返信になる。
type Command struct {
	Name string
	Desc string
	// NeedsSession が真のとき、会話がまだ無ければ実行せずにその旨を返す。
	// 何も無い場所で「消しました」と返っては、何が起きたのか分からない。
	NeedsSession bool
	Run          func(ctx context.Context, d *Deps, sess *store.Session, arg string) string
}

// List は使えるコマンド。並びがそのまま一覧に出る。
//
// 変数ではなく関数にしてあるのは、一覧を出すコマンド自身が一覧を読むためで
// ある。変数だと自分を参照する初期化になり、組み立てられない。
func List() []Command {
	return []Command{
		{
			Name: "stop", Desc: "走っている生成を止める", NeedsSession: true,
			Run: func(ctx context.Context, d *Deps, sess *store.Session, _ string) string {
				if !d.Runs.Cancel(sess.ID) {
					return "いま走っているものはありません。"
				}
				return "止めました。"
			},
		},
		{
			Name: "compact", Desc: "これまでのやり取りをまとめてコンテキストを空ける", NeedsSession: true,
			Run: func(ctx context.Context, d *Deps, sess *store.Session, arg string) string {
				// 走っている生成の入力を途中で組み替えることはできない。
				if d.Runs.Running(sess.ID) {
					return "生成中はまとめられません。先に /stop で止めてください。"
				}
				if d.Compact == nil {
					return "この入口ではまとめられません。"
				}
				res, err := d.Compact(ctx, sess.ID, arg)
				if err != nil {
					return "まとめられませんでした: " + err.Error()
				}
				return fmt.Sprintf(
					"%d 件をまとめました。元のやり取りは消えていないので、"+
						"巻き戻せば圧縮前に戻せます。", res.Summarized)
			},
		},
		{
			Name: "clear", Desc: "この会話の履歴をすべて消す", NeedsSession: true,
			Run: func(ctx context.Context, d *Deps, sess *store.Session, _ string) string {
				// 走っている最中に履歴を消すと、そのターンが自分の書き込み先を失う。
				if d.Runs.Running(sess.ID) {
					return "生成中は消せません。先に /stop で止めてください。"
				}
				got, err := d.Store.ClearMessages(ctx, sess.ID)
				if err != nil {
					return "消せませんでした: " + err.Error()
				}
				if got.Messages == 0 && got.Tickets == 0 {
					return "消すものはありませんでした。"
				}
				// 件数を返すのは、取り消せない操作だからである。何が消えたのかが
				// 数だけでも残らないと、打ち間違いに気づく手がかりが無い。
				msg := fmt.Sprintf("履歴を消しました (%d 件)", got.Messages)
				if got.Tickets > 0 {
					msg += fmt.Sprintf("。チケットも消しました (%d 件)", got.Tickets)
				}
				return msg + "。作業ディレクトリのファイルはそのままです。"
			},
		},
		{
			Name: "help", Desc: "使えるコマンドを出す",
			Run: func(ctx context.Context, d *Deps, sess *store.Session, _ string) string {
				return Help()
			},
		},
	}
}

// Help は一覧。知らないコマンドにもこれを返す。打ち間違えたときに、
// 何が使えるのかをその場で見せる。
func Help() string {
	var b strings.Builder
	b.WriteString("使えるコマンド:\n")
	for _, c := range List() {
		b.WriteString("- `/" + c.Name + "` — " + c.Desc + "\n")
	}
	b.WriteString("-# 先頭が / の本文はコマンドとして読みます。/ で始まる文をそのまま伝えたいときは、前に一言添えてください。")
	return b.String()
}

// Run はコマンドを実行し、返す文を返す。sess は呼び出し側が引く。Discord は
// チャンネルから、Web は開いている会話から引くので、引き方をここへは持たない。
func Run(ctx context.Context, d *Deps, sess *store.Session, name, arg string) string {
	for _, c := range List() {
		if c.Name != name {
			continue
		}
		if c.NeedsSession && sess == nil {
			return "まだ会話がありません。"
		}
		return c.Run(ctx, d, sess, arg)
	}
	return "`/" + name + "` は知らないコマンドです。\n" + Help()
}
