package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// コンテキスト長を明示しないと、提供元の既定 (Ollama は 4096) で走る。ツールの結果を
// 往復するだけで埋まり、埋まると古い側から黙って捨てられて指示文ごと失われる。
// 渡していることを、実際の要求の中身で確かめる。
func TestRequestCarriesContextLength(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("ok"), {Type: provider.EventDone}}
	})
	f.cfg.ContextTokens = 16384
	f.mock.ctxLen = 40960

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "こんにちは", f.emit); err != nil {
		t.Fatal(err)
	}
	got := f.mock.reqs[0].Options["num_ctx"]
	if got != 16384 {
		t.Errorf("num_ctx = %v, want 16384", got)
	}
}

// モデルが持てる以上を求めても意味がない。持てる値が分かるならそちらで頭を打つ。
func TestContextLengthCappedByModel(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("ok"), {Type: provider.EventDone}}
	})
	f.cfg.ContextTokens = 65536
	f.mock.ctxLen = 8192

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "こんにちは", f.emit); err != nil {
		t.Fatal(err)
	}
	if got := f.mock.reqs[0].Options["num_ctx"]; got != 8192 {
		t.Errorf("num_ctx = %v, want 8192", got)
	}
}

// 定義に書かれた値は利用者の意思なので、こちらの既定で上書きしない。
// また、定義そのものへ書き戻してもならない。定義はファイルが正である。
func TestAgentOptionWinsAndIsNotWrittenBack(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("ok"), {Type: provider.EventDone}}
	})
	f.cfg.ContextTokens = 16384
	f.mock.ctxLen = 40960
	main, _ := f.eng.Agents.Get("main")
	main.Options = map[string]any{"num_ctx": 2048}

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "こんにちは", f.emit); err != nil {
		t.Fatal(err)
	}
	if got := f.mock.reqs[0].Options["num_ctx"]; got != 2048 {
		t.Errorf("num_ctx = %v, want 2048", got)
	}
	if len(main.Options) != 1 {
		t.Errorf("定義の options が書き換えられている: %v", main.Options)
	}
}

// 割合の分母は、実際に渡した値でなければならない。モデルが持てる最大値で
// 割ると、まだ余裕があるように見えているうちに溢れる。
func TestUsageGaugeUsesWhatWeSend(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{
			text("ok"),
			{Type: provider.EventDone, Usage: &provider.Usage{PromptTokens: 1000}},
		}
	})
	f.cfg.ContextTokens = 16384
	f.mock.ctxLen = 131072

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "こんにちは", f.emit); err != nil {
		t.Fatal(err)
	}
	var limit int
	for _, ev := range f.events {
		if ev.Type == EvtUsage {
			limit = ev.ContextLimit
		}
	}
	if limit != 16384 {
		t.Errorf("分母 = %d, want 16384 (渡した値)", limit)
	}
	if sent := f.mock.reqs[0].Options["num_ctx"]; sent != limit {
		t.Errorf("渡した値 %v と分母 %d が食い違っている", sent, limit)
	}
}

// 上限に当たって止まったのか言い終えたのかは、伝えないと区別できない。
// 途中で切れた応答が、完成した答えとして残る。
func TestTruncatedGenerationIsReported(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{
			text("途中まで書いて"),
			{Type: provider.EventDone, Truncated: true},
		}
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "長い話をして", f.emit); err != nil {
		t.Fatal(err)
	}

	var note string
	for _, ev := range f.events {
		if ev.Type == EvtError {
			note = ev.Error
		}
	}
	if !strings.Contains(note, "打ち切られました") {
		t.Fatalf("打ち切りが伝わっていない: %q", note)
	}

	// 履歴にも残す。開き直したときに、途中で終わったことが分からなくなる。
	msgs, err := f.store.ListMessages(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Role == provider.RoleAssistant && strings.Contains(m.Error, "打ち切られました") {
			found = true
		}
	}
	if !found {
		t.Error("打ち切りが履歴に残っていない")
	}
}

// 言い終えたときに打ち切りの印を付けてはならない。付けると、正常な応答が
// 毎回失敗として見える。
func TestNormalGenerationIsNotReported(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("答えました"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "教えて", f.emit); err != nil {
		t.Fatal(err)
	}
	for _, ev := range f.events {
		if ev.Type == EvtError {
			t.Fatalf("正常な応答に失敗が付いている: %q", ev.Error)
		}
	}
}
