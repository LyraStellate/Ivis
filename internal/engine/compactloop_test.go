package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// ターンの終わりにしか圧縮しないと、1 ターンの中でツールを往復して溢れる
// 場合に効かない。反復の先頭で見る (#486237)。

// script を書きやすくするための道具。要約の生成は道具を渡さないので、
// そこで見分けられる。
func isSummaryReq(req provider.Request) bool { return len(req.Tools) == 0 }

func usedTokens(n int) provider.Event {
	return provider.Event{Type: provider.EventDone, Usage: &provider.Usage{PromptTokens: n}}
}

// 反復の途中で上限に近づいたら、そこでまとめて入力を組み直す。
func TestCompactsBetweenToolCalls(t *testing.T) {
	summaries := 0
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if isSummaryReq(req) {
			summaries++
			return []provider.Event{text("ここまでのまとめ"), {Type: provider.EventDone}}
		}
		if summaries == 0 {
			// まだまとめていない。道具を呼び、使用量を 85% で申告する。
			return []provider.Event{
				callTool("list_dir", map[string]any{"path": "."}),
				usedTokens(850),
			}
		}
		return []provider.Event{text("続きを終えました"), {Type: provider.EventDone}}
	})
	f.cfg.ContextTokens = 1000
	id := f.newSession(t, "main")

	if err := f.eng.Run(context.Background(), id, "長い作業", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summaries != 1 {
		t.Fatalf("まとめが %d 回", summaries)
	}

	// まとめたあとの入力は、要約と続きを促す 1 通だけになっている。積み上げた
	// 往復がそのまま残っていたら、まとめた意味が無い。
	last := f.mock.reqs[len(f.mock.reqs)-1].Messages
	var roles []string
	for _, m := range last {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "system,system,user" {
		t.Fatalf("まとめたあとの入力 = %v", roles)
	}
	if !strings.Contains(last[1].Content, "ここまでのまとめ") {
		t.Errorf("要約が渡っていない: %q", last[1].Content)
	}
	// 引き直した並びには、いま何をしているかを促すものが 1 つも無い。
	if !strings.Contains(last[2].Content, "続き") {
		t.Errorf("続きを促していない: %q", last[2].Content)
	}

	// 繕った 1 通は会話に残さない。残すと、利用者が書いていない発言が
	// 履歴に並ぶ。
	msgs, _ := f.store.ListMessages(context.Background(), id)
	for _, m := range msgs {
		if strings.Contains(m.Content, "続きから進めてください") {
			t.Error("繕った 1 通が履歴に残っている")
		}
	}
}

// 閾値はターンの終わりより早い。反復の中で読める使用量は 1 往復ぶん古く、
// いま積んだツールの結果が入っていないためである。
func TestMidLoopThreshold(t *testing.T) {
	cases := []struct {
		name   string
		tokens int
		want   bool
	}{
		{"79% では走らない", 790, false},
		{"80% で走る", 800, true},
		{"ターンの終わりの 90% より早い", 850, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ran := false
			f := newFixture(t, func(n int, req provider.Request) []provider.Event {
				if isSummaryReq(req) {
					ran = true
					return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
				}
				if n == 0 {
					return []provider.Event{
						callTool("list_dir", map[string]any{"path": "."}),
						usedTokens(c.tokens),
					}
				}
				return []provider.Event{text("終わり"), {Type: provider.EventDone}}
			})
			f.cfg.ContextTokens = 1000
			id := f.newSession(t, "main")

			if err := f.eng.Run(context.Background(), id, "作業", f.emit); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if ran != c.want {
				t.Fatalf("途中でまとめた = %v, want %v", ran, c.want)
			}
		})
	}
}

// 委譲した子ではまとめない。使用量を書いているのは親のターンなので読む値は
// 親のものだし、子の中でまとめると親の入力が古くなる。そのとき親はツールの
// 往復の最中で、入力を組み替えてよい境界に居ない。
func TestMidLoopCompactSkipsDelegates(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := f.newSession(t, "main")
	exchanges(t, f, id, 8)
	if err := f.store.SetContextUsage(ctx, id, 9900, 10000); err != nil {
		t.Fatal(err)
	}

	ag, _ := f.agents.Get("main")
	rc := &runCtx{sessionID: id, kind: "main", agent: ag, depth: 1, emit: f.emit}
	if f.eng.compactMidLoop(ctx, rc) {
		t.Fatal("子の中でまとめてしまった")
	}
	if f.mock.calls != 0 {
		t.Errorf("生成が %d 回", f.mock.calls)
	}
}

// 打ち切られたら、まとめて 1 度だけやり直す。やり直さずに次へ入っても入力の
// 大きさは変わらないので、同じ場所で溢れ直すだけである。
func TestCutRetriesOnceAfterCompacting(t *testing.T) {
	summaries, cuts := 0, 0
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if isSummaryReq(req) {
			summaries++
			return []provider.Event{text("ここまでのまとめ"), {Type: provider.EventDone}}
		}
		if summaries == 0 {
			cuts++
			return []provider.Event{text("途中まで"), {Type: provider.EventDone, Truncated: true}}
		}
		return []provider.Event{text("まとめ直して答えました"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")
	exchanges(t, f, id, 8)

	if err := f.eng.Run(context.Background(), id, "長い話", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cuts != 1 || summaries != 1 {
		t.Fatalf("打ち切り %d 回・まとめ %d 回", cuts, summaries)
	}
	// やり直して答えられたのだから、失敗として見せてはいけない。
	for _, ev := range f.events {
		if ev.Type == EvtError {
			t.Errorf("やり直せたのにエラーを出した: %q", ev.Error)
		}
	}
	// 何が起きたかは伝える。黙って会話の見え方が変わると、応答が変わった
	// 理由が読めない。
	told := false
	for _, ev := range f.events {
		if ev.Type == EvtNotice && strings.Contains(ev.Text, "続きから進めます") {
			told = true
		}
	}
	if !told {
		t.Error("まとめ直したことを伝えていない")
	}
}

// まとめても入らないなら、繰り返しても同じである。1 度で諦めて案内を出す。
func TestCutGivesUpAfterOneRetry(t *testing.T) {
	summaries, cuts := 0, 0
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if isSummaryReq(req) {
			summaries++
			return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
		}
		cuts++
		return []provider.Event{text("途中まで"), {Type: provider.EventDone, Truncated: true}}
	})
	id := f.newSession(t, "main")
	exchanges(t, f, id, 8)

	if err := f.eng.Run(context.Background(), id, "長い話", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cuts != 2 || summaries != 1 {
		t.Fatalf("打ち切り %d 回・まとめ %d 回", cuts, summaries)
	}
	var note string
	for _, ev := range f.events {
		if ev.Type == EvtError {
			note = ev.Error
		}
	}
	for _, want := range []string{"打ち切られました", "会話を分けて"} {
		if !strings.Contains(note, want) {
			t.Errorf("案内に %q がありません: %q", want, note)
		}
	}
}

// まとめる分が無ければ、やり直さずにそのまま案内する。要約の要約を作り続けても
// 何も減らない。
func TestCutWithNothingToCompactDoesNotRetry(t *testing.T) {
	gens := 0
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		gens++
		return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := f.newSession(t, "main")
	// 1 往復を積んでまとめる。これで残るのは要約 1 件だけになり、まとめる
	// 分が無い状態になる。
	exchanges(t, f, id, 1)
	if _, err := f.eng.Compact(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}
	gens = 0
	f.events = nil

	ag, _ := f.agents.Get("main")
	rc := &runCtx{sessionID: id, kind: "main", agent: ag, emit: f.emit}
	if f.eng.compactAfterCut(ctx, rc) {
		t.Fatal("まとめる分が無いのにまとめたことにした")
	}
	if gens != 0 {
		t.Errorf("生成が %d 回", gens)
	}
	// 打つ手が無いことで騒がない。
	for _, ev := range f.events {
		if ev.Type == EvtNotice && strings.Contains(ev.Text, "まとめられませんでした") {
			t.Errorf("打つ手の無いことを知らせている: %q", ev.Text)
		}
	}
}
