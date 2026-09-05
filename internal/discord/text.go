package discord

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// 引数のうち、行に載せるものを選ぶ順。何をしようとしているかが一番よく
// 表れる欄を先に見る。どれも無ければ名前順で最初のものを使う。
var detailKeys = []string{"command", "query", "url", "path", "pattern", "task",
	"agent_id", "text", "name"}

// summarize はツールの引数を 1 行の説明にする。
func summarize(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, k := range detailKeys {
		if v, ok := args[k]; ok {
			if s := valueText(v); s != "" {
				return "`" + clip(oneLine(s), detailMax) + "`"
			}
		}
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if s := valueText(args[keys[0]]); s != "" {
		return "`" + clip(oneLine(s), detailMax) + "`"
	}
	return ""
}

func valueText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// oneLine は改行を潰して 1 行にする。
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip は長さで切る。多バイト文字の途中で切らないよう文字単位で数える。
func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:max-1]), " ") + "…"
}

// tail は末尾だけを残す。推論のように、いま出ているところだけ見せたい
// ものに使う。
func tail(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return "…" + string(r[len(r)-max:])
}

// split は本文を Discord の上限に収まる塊へ分ける。
//
// 切れ目は行の境界を選ぶ。コードブロックの内側で切るときは、そこで閉じて
// 次の塊で開き直す。閉じないまま切ると、続きのメッセージ全体がコードとして
// 表示される。
func split(s string, limit int) []string {
	if utf8.RuneCountInString(s) <= limit {
		return []string{s}
	}

	var out []string
	var cur []string
	n := 0
	// open は開いているコードブロックの開始行。空なら外側。lead は引用の
	// 印など、その行の前に付いていたもので、閉じ直すときに同じものを付ける。
	open, lead := "", ""

	flush := func() {
		if len(cur) == 0 {
			return
		}
		part := strings.Join(cur, "\n")
		if open != "" {
			part += "\n" + lead + "```"
		}
		out = append(out, part)
		cur = cur[:0]
		n = 0
		if open != "" {
			cur = append(cur, open)
			n = utf8.RuneCountInString(open) + 1
		}
	}

	for _, line := range strings.Split(s, "\n") {
		for _, piece := range hardSplit(line, limit-8) {
			w := utf8.RuneCountInString(piece) + 1
			if n+w > limit-8 {
				flush()
			}
			cur = append(cur, piece)
			n += w
			if p, ok := fenceOf(piece); ok {
				if open == "" {
					open, lead = strings.TrimRight(piece, " "), p
				} else {
					open, lead = "", ""
				}
			}
		}
	}
	if len(cur) > 0 {
		part := strings.Join(cur, "\n")
		if strings.TrimSpace(part) != "" && strings.TrimSpace(part) != open {
			out = append(out, part)
		}
	}
	return out
}

// hardSplit は 1 行が上限を超える場合に、文字単位で切る。切れ目を選べない
// 入力 (長大な 1 行) でも、送れない塊を作らないための最後の手段。
func hardSplit(line string, limit int) []string {
	if utf8.RuneCountInString(line) <= limit {
		return []string{line}
	}
	var out []string
	r := []rune(line)
	for len(r) > limit {
		out = append(out, string(r[:limit]))
		r = r[limit:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// fenceOf は行がコードブロックの区切りなら、その前に付いている印を返す。
// 引用の中の区切りも見落とさないために要る。見落とすと、引用ごと途中で
// 切ったときにコードブロックが開いたままになる。
func fenceOf(line string) (lead string, ok bool) {
	t := strings.TrimSpace(line)
	for strings.HasPrefix(t, ">") {
		t = strings.TrimSpace(strings.TrimPrefix(t, ">"))
		lead += "> "
	}
	return lead, strings.HasPrefix(t, "```")
}
