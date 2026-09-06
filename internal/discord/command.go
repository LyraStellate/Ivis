package discord

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/LyraStellate/Ivis/internal/store"
)

// コマンドは、呼びかけの本文の先頭が / かどうかで見分ける。
//
// Discord のスラッシュコマンドとして登録しない。登録には招待に
// applications.commands が要り、既に招待済みのボットでは入れ直しになる。
// サーバーごとの登録は反映が遅れることもあり、入れた直後に止められないので
// あれば緊急停止の役に立たない。
//
// 見分けを本文で行えば、決まりは 1 つで済む — 用があるときは必ずメンションし、
// 先頭に / があればそれは Ivis への指示で、無ければエージェントへの依頼である。

// parseCommand は本文からコマンド名と、その後ろを返す。
//
// 名前は英字だけとし、大文字小文字は問わない。/Stop と /stop を別のものに
// すると、打った本人にはどちらを打ったか見えているので、効かない理由が
// 分からない。
func parseCommand(text string) (name, rest string, ok bool) {
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

// command は 1 つのコマンド。返した文がそのまま返信になる。
type command struct {
	name string
	desc string
	// needsSession が真のとき、会話がまだ無ければ実行せずにその旨を返す。
	// 何も無い場所で「消しました」と返っては、何が起きたのか分からない。
	needsSession bool
	run          func(b *Bridge, ctx context.Context, sess *store.Session, arg string) string
}

// commands は使えるコマンド。並びがそのまま一覧に出る。
//
// 変数ではなく関数にしてあるのは、一覧を出すコマンド自身が一覧を読むためで
// ある。変数だと自分を参照する初期化になり、組み立てられない。
func commands() []command {
	return []command{
		{
			name: "stop", desc: "走っている生成を止める", needsSession: true,
			run: func(b *Bridge, ctx context.Context, sess *store.Session, _ string) string {
				if !b.stopSession(sess.ID) {
					return "いま走っているものはありません。"
				}
				return "止めました。"
			},
		},
		{
			name: "clear", desc: "このチャンネルの会話の履歴をすべて消す", needsSession: true,
			run: func(b *Bridge, ctx context.Context, sess *store.Session, _ string) string {
				// 走っている最中に履歴を消すと、そのターンが自分の書き込み先を失う。
				if b.deps.Runs.Running(sess.ID) {
					return "生成中は消せません。先に /stop で止めてください。"
				}
				n, err := b.deps.Store.ClearMessages(ctx, sess.ID)
				if err != nil {
					return "消せませんでした: " + err.Error()
				}
				if n == 0 {
					return "消す履歴はありませんでした。"
				}
				// 件数を返すのは、取り消せない操作だからである。何が消えたのかが
				// 数だけでも残らないと、打ち間違いに気づく手がかりが無い。
				return fmt.Sprintf("履歴を消しました (%d 件)。作業ディレクトリのファイルはそのままです。", n)
			},
		},
		{
			name: "help", desc: "使えるコマンドを出す",
			run: func(b *Bridge, ctx context.Context, sess *store.Session, _ string) string {
				return helpText()
			},
		},
	}
}

// helpText は一覧。知らないコマンドにもこれを返す。打ち間違えたときに、
// 何が使えるのかをその場で見せる。
func helpText() string {
	var b strings.Builder
	b.WriteString("使えるコマンド:\n")
	for _, c := range commands() {
		b.WriteString("- `/" + c.name + "` — " + c.desc + "\n")
	}
	b.WriteString("-# 先頭が / の呼びかけはコマンドとして読みます。/ で始まる文をそのまま伝えたいときは、前に一言添えてください。")
	return b.String()
}

// runCommand はコマンドを実行し、返す文を返す。
func (b *Bridge) runCommand(ctx context.Context, in incoming, name, arg string) string {
	for _, c := range commands() {
		if c.name != name {
			continue
		}
		var sess *store.Session
		if c.needsSession {
			s, err := b.deps.Store.SessionByChannel(ctx, in.ChannelID)
			if err != nil {
				return "このチャンネルにはまだ会話がありません。"
			}
			sess = s
		}
		return c.run(b, ctx, sess, arg)
	}
	return "`/" + name + "` は知らないコマンドです。\n" + helpText()
}
