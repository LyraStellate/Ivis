package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// fakeAsker は問いに決まった答えを返す。
type fakeAsker struct {
	got    Question
	answer string
	calls  int
}

func (a *fakeAsker) Ask(ctx context.Context, q Question) (string, error) {
	a.got = q
	a.calls++
	return a.answer, nil
}

// 問いへの答えは、ツールの結果としてそのまま次の生成へ渡る。渡らないと、
// モデルは訊いた意味を失い、同じことをもう一度訊く。
func TestAskUserReturnsAnswer(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{
				callTool("ask_user", map[string]any{
					"question": "どちらの形式で出しますか",
					"choices":  []any{"JSON", "CSV"},
				}),
				{Type: provider.EventDone},
			}
		}
		return []provider.Event{text("わかりました"), {Type: provider.EventDone}}
	})
	asker := &fakeAsker{answer: "CSV でお願いします"}
	f.eng.Asker = asker

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "出力して", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if asker.calls != 1 {
		t.Fatalf("問いの回数 = %d, want 1", asker.calls)
	}
	if asker.got.Text != "どちらの形式で出しますか" {
		t.Errorf("問い = %q", asker.got.Text)
	}
	if len(asker.got.Choices) != 2 || asker.got.Choices[0] != "JSON" {
		t.Errorf("候補 = %v", asker.got.Choices)
	}
	if asker.got.ToolCallID == "" {
		t.Error("呼び出しの識別子が付いていない。問いをどの行の下に出すか決まらない")
	}

	// 2 回目の生成に、答えがツールの結果として載っていること。
	last := f.mock.reqs[len(f.mock.reqs)-1]
	found := false
	for _, m := range last.Messages {
		if m.Role == provider.RoleTool && strings.Contains(m.Content, "CSV でお願いします") {
			found = true
		}
	}
	if !found {
		t.Error("答えが次の生成へ渡っていない")
	}

	// 画面が問いを描けるよう、イベントが流れていること。
	var q *Question
	for _, ev := range f.events {
		if ev.Type == EvtQuestion {
			q = ev.Question
		}
	}
	if q == nil {
		t.Fatal("question のイベントが流れていない")
	}
	if q.SessionID != id {
		t.Errorf("会話の識別子 = %q, want %q", q.SessionID, id)
	}
}

// 問える相手が居なくても、ターンは進む。ここで失敗にすると、常駐からの実行が
// 問いに当たるたびに丸ごと落ちる。
func TestAskUserWithoutAskerContinues(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{
				callTool("ask_user", map[string]any{"question": "どうしますか"}),
				{Type: provider.EventDone},
			}
		}
		return []provider.Event{text("自分で決めました"), {Type: provider.EventDone}}
	})
	f.eng.Asker = nil

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "やって", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := f.mock.reqs[len(f.mock.reqs)-1]
	found := false
	for _, m := range last.Messages {
		if m.Role == provider.RoleTool && strings.Contains(m.Content, "自分で選んで進め") {
			found = true
		}
	}
	if !found {
		t.Error("問えないことがモデルへ伝わっていない")
	}
}

// 空の答えは「決めてよい」と読む。そのまま返すと、モデルは空文字を答えとして
// 扱い、何も決まらないまま進む。
func TestAskUserEmptyAnswer(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{
				callTool("ask_user", map[string]any{"question": "どうしますか"}),
				{Type: provider.EventDone},
			}
		}
		return []provider.Event{text("進めました"), {Type: provider.EventDone}}
	})
	f.eng.Asker = &fakeAsker{answer: "   "}

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "やって", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := f.mock.reqs[len(f.mock.reqs)-1]
	for _, m := range last.Messages {
		if m.Role == provider.RoleTool && !strings.Contains(m.Content, "自分で選んで進め") {
			t.Errorf("空の答えがそのまま渡っている: %q", m.Content)
		}
	}
}

// 指示文は、問う手段の有無で書き分ける。持っていない道具を勧めると、モデルは
// 代わりに本文で問いかけ、そのターンは何も進まないまま終わる。
func TestSystemPromptStatesHowToProceed(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("はい"), {Type: provider.EventDone}}
	})
	main, _ := f.eng.Agents.Get("main")
	child, _ := f.eng.Agents.Get("child")

	withAsk := systemPrompt(main, nil, nil, "/w", time.Now())
	if !strings.Contains(withAsk, "ask_user") {
		t.Error("問える相手にその手段が示されていない")
	}
	if !strings.Contains(withAsk, "最後までやり切って") {
		t.Error("やり切ることが指示文に無い")
	}

	noAsk := systemPrompt(child, nil, nil, "/w", time.Now())
	if strings.Contains(noAsk, "ask_user") {
		t.Error("使えない道具を勧めている")
	}
	if !strings.Contains(noAsk, "問い返す手段はありません") {
		t.Error("問えないことが示されていない")
	}
}
