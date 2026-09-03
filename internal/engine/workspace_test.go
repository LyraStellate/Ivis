package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
)

// 会話ごとに作業場所が分かれる。同じ名前で書いても互いを上書きしない。
func TestWorkspaceIsPerSession(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n%2 == 0 {
			return []provider.Event{callTool("write_file", map[string]any{
				"path": "out.txt", "content": "session " + req.Messages[len(req.Messages)-1].Content,
			})}
		}
		return []provider.Event{text("書きました"), {Type: provider.EventDone}}
	})

	a := f.newSession(t, "main")
	b := f.newSession(t, "main")
	for _, id := range []string{a, b} {
		if err := f.eng.Run(context.Background(), id, id, f.emit); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	for _, id := range []string{a, b} {
		p := filepath.Join(f.cfg.WorkspaceDir, config.SeriesDir, id, "out.txt")
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s に書かれていません: %v", p, err)
		}
		if string(got) != "session "+id {
			t.Errorf("%s の中身 = %q", p, got)
		}
	}

	// 作業ディレクトリの直下には何も置かない。混ざる場所を残さない。
	if _, err := os.Stat(filepath.Join(f.cfg.WorkspaceDir, "out.txt")); err == nil {
		t.Error("共通の作業ディレクトリへ書かれています")
	}
}

// 指示文には、その会話の作業場所を書く。共通の場所を書くと、モデルは
// 実際には触れない場所を案内される。
func TestSystemPromptShowsSessionWorkspace(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("ok"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "やあ", f.emit); err != nil {
		t.Fatal(err)
	}
	sys := f.mock.reqs[0].Messages[0].Content
	want := filepath.Join(f.cfg.WorkspaceDir, config.SeriesDir, id)
	if !strings.Contains(sys, want) {
		t.Errorf("指示文に会話の作業場所がありません: %q", want)
	}
}

// 1 ターン使った文脈の量を記録し、画面へ流す。
func TestContextUsageIsRecorded(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{
			text("ok"),
			{Type: provider.EventDone, Usage: &provider.Usage{PromptTokens: 320, EvalTokens: 12}},
		}
	})
	f.mock.ctxLen = 8192
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "やあ", f.emit); err != nil {
		t.Fatal(err)
	}

	sess, err := f.store.GetSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ContextTokens != 320 || sess.ContextLimit != 8192 {
		t.Errorf("使用量 = %d/%d, want 320/8192", sess.ContextTokens, sess.ContextLimit)
	}
	if !strings.Contains(strings.Join(f.typesOf(), ","), EvtUsage) {
		t.Error("使用量のイベントが流れていません")
	}
}

// 定義に num_ctx があれば、提供元へ問い合わせずそれを分母にする。
func TestContextLimitPrefersAgentOption(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{
			text("ok"),
			{Type: provider.EventDone, Usage: &provider.Usage{PromptTokens: 10}},
		}
	})
	f.mock.ctxLen = 8192
	ag, _ := f.eng.Agents.Get("main")
	ag.Options = map[string]any{"num_ctx": 2048}

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "やあ", f.emit); err != nil {
		t.Fatal(err)
	}
	sess, _ := f.store.GetSession(context.Background(), id)
	if sess.ContextLimit != 2048 {
		t.Errorf("文脈長 = %d, want 2048", sess.ContextLimit)
	}
}

// 委譲された子では記録しない。見せているのは利用者が次に送れる量である。
func TestContextUsageIgnoresChildren(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		switch n {
		case 0:
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": "child", "task": "任せる",
			})}
		case 1: // 子。ここの量は数えない。
			return []provider.Event{
				text("できました"),
				{Type: provider.EventDone, Usage: &provider.Usage{PromptTokens: 9999}},
			}
		default:
			return []provider.Event{
				text("受け取りました"),
				{Type: provider.EventDone, Usage: &provider.Usage{PromptTokens: 500}},
			}
		}
	})
	f.mock.ctxLen = 4096
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "頼む", f.emit); err != nil {
		t.Fatal(err)
	}
	sess, _ := f.store.GetSession(context.Background(), id)
	if sess.ContextTokens != 500 {
		t.Errorf("使用量 = %d, want 500 (子の分を数えない)", sess.ContextTokens)
	}
}
