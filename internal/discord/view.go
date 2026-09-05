package discord

import (
	"fmt"
	"strings"
	"time"
)

// msg は 1 通のメッセージのあるべき姿。key はどのメッセージかを表す名前で、
// 同じ key は同じメッセージを指し続ける。
type msg struct {
	key string
	// reply が真なら、呼びかけたメッセージへのリプライとして投稿する。
	reply bool
	p     Payload
}

// view はいま Discord に並んでいるべきメッセージを返す。
//
// 経過も回答も 1 通に収め、書き換えながら伸ばす。枠 (埋め込み) は使わない。
// 分かれるのは、上限を超えて 1 通に収まらないときだけである。
//
// 何が回答で何がそうでないかは書式で分ける。回答だけが飾りの無い地の文で、
// それ以外はすべて何らかの形で囲われている。
func (t *turn) view(now time.Time) []msg {
	var lines []string
	add := func(s string) {
		if s != "" {
			lines = append(lines, s)
		}
	}

	for i, it := range t.items {
		// 委譲された先の出来事は、その委譲の塊の中で描く。
		if it.parent >= 0 && it.kind != kindDelegate {
			continue
		}
		switch it.kind {
		case kindThink:
			add(t.thinkText(it))
		case kindAnswer:
			body := t.answerText(it)
			if body == "" {
				continue
			}
			// 回答の前は 1 行空ける。飾りの無い地の文であることに加えて、
			// 間が空いていれば、経過の続きではないと目で分かる。
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			add(body)
		case kindTool:
			add(toolLine(it))
		case kindDelegate:
			add(t.delegateBlock(i, it))
		}
	}

	if t.err != "" {
		add("**失敗:** " + t.err + hintLine(t.errKind))
	}
	if t.stopped {
		add("-# 停止しました")
	}

	body := strings.Join(lines, "\n")
	if strings.TrimSpace(body) == "" {
		if !t.done {
			return nil
		}
		// 黙って終わると、呼びかけが届いていないのか、届いて何も返らなかった
		// のかが区別できない。
		body = "-# 応答がありませんでした"
	}

	parts := split(body, bodyLimit)
	out := make([]msg, 0, len(parts))
	for n, p := range parts {
		out = append(out, msg{key: fmt.Sprintf("log:%d", n), reply: n == 0,
			p: Payload{Content: p}})
	}
	// 押しボタンは最後の通に付ける。伸びていく先がそこなので、承認を求めた
	// 場所が画面の外へ出ない。
	if b := t.buttons(); len(b) > 0 {
		out[len(out)-1].p.Buttons = b
	}
	return out
}

// buttons は承認を待っているツールの押しボタンを返す。
func (t *turn) buttons() []Button {
	for _, it := range t.items {
		if !it.waiting || it.approvalID == "" {
			continue
		}
		return []Button{
			{ID: approveID(it.approvalID, true), Label: "実行する"},
			{ID: approveID(it.approvalID, false), Label: "やめる", Danger: true},
		}
	}
	return nil
}

// thinkText は推論。伸びている間は全文を出し、終われば長さだけへ縮める。
// 途中を省いてしまうと、何を考えていたかを追う手段が無くなる。
//
// 区切った塊として出すのは、これが結論ではないからである。回答と同じ地の
// 文で並ぶと、どちらを読めばよいか分からなくなる。
func (t *turn) thinkText(it *item) string {
	if it.closed() {
		return fence("推論: " + elapsed(it.end.Sub(it.start)))
	}
	body := strings.TrimRight(it.text.String(), " \n")
	if strings.TrimSpace(body) == "" {
		body = "推論中"
	}
	return fence(body)
}

// answerText は回答。生成中は末尾に印を付け、まだ書いている途中であることと
// 書き終えたこととを区別する。ここだけが飾りの無い地の文になる。
func (t *turn) answerText(it *item) string {
	body := strings.TrimRight(it.text.String(), " \n")
	if body == "" {
		return ""
	}
	if !it.closed() {
		body += " ▍"
	}
	return body
}

// ツールの段階。名前と揃えて 1 つの塊に収めるので、語も英語にする。
const (
	stageRunning  = "running"
	stageApproval = "approval"
	stageQuestion = "question"
	stageComplete = "complete"
	stageFailed   = "failed"
	stageAborted  = "aborted"
)

// toolLine はツール 1 回分。名前と段階を 1 つの塊にまとめる。絵文字は
// 使わない。結果を待たずに終わったものを complete と書かないのは、止めたのに
// 終わったように見えては、何が起きたかを取り違えるためである。
func toolLine(it *item) string {
	line := code(it.name + " : " + stageOf(it))
	// 引数と理由だけは塊の外へ出す。承認は何を通すのかが無ければ選べず、
	// 失敗は理由が無ければ次の手が決まらない。どちらも長さが読めないので、
	// 塊に混ぜると 1 行の見た目が崩れる。
	if it.detail != "" && (it.waiting || it.failed) {
		line += "\n-# " + oneLine(it.detail)
	}
	return line
}

func stageOf(it *item) string {
	switch {
	case it.failed:
		return stageFailed
	case it.questionID != "":
		return stageQuestion
	case it.waiting:
		return stageApproval
	case it.aborted:
		return stageAborted
	case it.closed():
		return stageComplete
	}
	return stageRunning
}

// delegateBlock は委譲 1 回分。任された先の出来事を引用として一段内へ寄せ、
// どこからどこまでが誰の作業かを示す。走り終わったら 1 行へ単純化する。
func (t *turn) delegateBlock(at int, it *item) string {
	head := "**" + t.nameOf(it.agent) + " へ委譲**"
	if it.closed() {
		if it.aborted {
			return quote(head + " (中断)")
		}
		return quote(head)
	}

	lines := []string{head}
	for _, in := range t.items {
		if in.parent != at {
			continue
		}
		if l := t.inner(in); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 1 && it.detail != "" {
		lines = append(lines, "-# "+it.detail)
	}
	return quote(strings.Join(lines, "\n"))
}

// inner は委譲の中の 1 つ。外と同じ見せ方をする。
func (t *turn) inner(it *item) string {
	switch it.kind {
	case kindThink:
		return t.thinkText(it)
	case kindAnswer:
		return t.answerText(it)
	case kindTool:
		return toolLine(it)
	case kindDelegate:
		// 入れ子の委譲は、それ自身の塊を下に持つ。ここでは起きたことだけ書く。
		return "**" + t.nameOf(it.agent) + " へ委譲**"
	}
	return ""
}

// code はそのまま読ませたい短い文字列を囲う。囲いに使う記号が中に混ざると
// そこで囲いが切れるので、取り除く。
// fence は塊ごと区切って出す。中に区切りと同じ記号が混ざるとそこで
// 切れるので、取り除く。
func fence(s string) string {
	s = strings.ReplaceAll(s, "```", "'''")
	return "```\n" + s + "\n```"
}

func code(s string) string {
	s = strings.ReplaceAll(s, "`", "'")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return "`" + s + "`"
}

// quote は塊ごと一段内へ寄せる。空行も含めて全ての行に印を付けるのは、
// 印の無い行が挟まると、そこで引用が切れて外の話に戻って見えるためである。
func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// hintLine は失敗の種類ごとに、次に取るべき行動を添える。まとめて
// 「エラーが発生しました」にすると、起動忘れなのか設定の誤りなのかが
// 利用者に判断できない。
func hintLine(kind string) string {
	var s string
	switch kind {
	case "provider_unavailable":
		s = "Ollama に繋がりません。起動しているか確かめてください。"
	case "model_not_found":
		s = "モデルがありません。ollama pull で取得してください。"
	case "tools_unsupported":
		s = "このモデルはツール呼び出しに対応していません。"
	case "thinking_unsupported":
		s = "このモデルは推論に対応していません。エージェント設定で切ってください。"
	}
	if s == "" {
		return ""
	}
	return "\n-# " + s
}

func elapsed(d time.Duration) string {
	if d < time.Second {
		return "1 秒未満"
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0f 秒", d.Seconds())
	}
	return fmt.Sprintf("%d 分 %d 秒", int(d.Minutes()), int(d.Seconds())%60)
}
