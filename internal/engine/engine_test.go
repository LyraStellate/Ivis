package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
)

func TestRunPlainAnswer(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("こんにちは"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")

	if err := f.eng.Run(context.Background(), id, "やあ", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, err := f.store.ListMessages(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("発言数 = %d, want 2 (user, assistant)", len(msgs))
	}
	if msgs[1].Content != "こんにちは" {
		t.Errorf("応答が保存されていません: %q", msgs[1].Content)
	}
	if got := f.typesOf(); got[len(got)-1] != EvtDone {
		t.Errorf("最後のイベント = %q, want done", got[len(got)-1])
	}
}

// スキル本文を毎ターン載せない設計の確認。プロンプトに載るのは名前と説明だけ。
func TestSystemPromptListsToolsAndDelegates(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("ok"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "やあ", f.emit); err != nil {
		t.Fatal(err)
	}

	req := f.mock.reqs[0]
	if req.Messages[0].Role != provider.RoleSystem {
		t.Fatalf("先頭が system ではありません: %q", req.Messages[0].Role)
	}
	if !strings.Contains(req.Messages[0].Content, "child") {
		t.Error("委譲先が指示文に列挙されていません")
	}
	// 一覧は Tier で絞る。呼べない相手を載せると、モデルは呼べるものとして
	// 選び、断られる往復が増える。
	if strings.Contains(req.Messages[0].Content, "main (Tier") {
		t.Error("自分自身が委譲先として載っています")
	}
	// 定義で許可したツールだけが渡る。
	names := map[string]bool{}
	for _, d := range req.Tools {
		names[d.Name] = true
	}
	if !names["list_dir"] || names["run_skill_script"] {
		t.Errorf("渡されたツールが定義と一致しません: %v", names)
	}
}

func TestToolLoopExecutesAndContinues(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{callTool("list_dir", map[string]any{"path": "."})}
		}
		return []provider.Event{text("空でした"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")

	if err := f.eng.Run(context.Background(), id, "中を見て", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, _ := f.store.ListMessages(context.Background(), id)
	var roles []string
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	want := []string{"user", "assistant", "tool", "assistant"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("役割の並び = %v, want %v", roles, want)
	}
	// ツールの結果がモデルへ戻っていること。
	if f.mock.calls != 2 {
		t.Errorf("生成回数 = %d, want 2", f.mock.calls)
	}
	last := f.mock.reqs[1].Messages
	if last[len(last)-1].Role != provider.RoleTool {
		t.Error("ツールの結果が次の入力に含まれていません")
	}
}

// ローカルモデルは同じツールを呼び続けることがある。打ち切れることを確かめる。
func TestIterationCapStopsRunawayLoop(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{callTool("list_dir", map[string]any{"path": "."})}
	})
	id := f.newSession(t, "main")

	err := f.eng.Run(context.Background(), id, "繰り返して", f.emit)
	if err == nil {
		t.Fatal("上限に達しても打ち切られませんでした")
	}
	if f.mock.calls != f.cfg.MaxIterations {
		t.Errorf("生成回数 = %d, want %d", f.mock.calls, f.cfg.MaxIterations)
	}
	if !strings.Contains(err.Error(), "打ち切りました") {
		t.Errorf("理由が伝わりません: %v", err)
	}
}

func TestUnknownToolIsReportedToModel(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{callTool("run_skill_script", map[string]any{"skill": "x", "script": "y"})}
		}
		return []provider.Event{text("諦めます"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "実行して", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, _ := f.store.ListMessages(context.Background(), id)
	var toolMsg string
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			toolMsg = m.Content
		}
	}
	// 許可されていないツールでも、こちらで打ち切らずモデルへ返して続けさせる。
	if !strings.Contains(toolMsg, "許可されていません") {
		t.Errorf("拒否の理由がモデルへ返っていません: %q", toolMsg)
	}
}

// 生成が途中で失敗しても、それまでの出力は失われない。
func TestPartialOutputIsPersistedOnError(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{
			text("ここまでは"),
			text("書けた"),
			{Type: provider.EventError, Err: errBroken},
		}
	})
	id := f.newSession(t, "main")

	if err := f.eng.Run(context.Background(), id, "書いて", f.emit); err == nil {
		t.Fatal("エラーが返っていません")
	}

	msgs, _ := f.store.ListMessages(context.Background(), id)
	var asst *storeMessage
	for _, m := range msgs {
		if m.Role == provider.RoleAssistant {
			asst = &storeMessage{Content: m.Content, Error: m.Error}
		}
	}
	if asst == nil {
		t.Fatal("応答が保存されていません")
	}
	if asst.Content != "ここまでは書けた" {
		t.Errorf("部分出力 = %q, want %q", asst.Content, "ここまでは書けた")
	}
	if asst.Error == "" {
		t.Error("失敗した事実が記録されていません")
	}
}

type storeMessage struct {
	Content string
	Error   string
}

var errBroken = errBrokenType{}

type errBrokenType struct{}

func (errBrokenType) Error() string { return "接続が切れました" }

// 下位のエージェントからは上位が見えない。呼べない相手を指示文へ載せると、
// モデルはそれを選び、断られる往復だけが増える。
func TestSystemPromptHidesHigherTiers(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("ok"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "child")
	if err := f.eng.Run(context.Background(), id, "やあ", f.emit); err != nil {
		t.Fatal(err)
	}

	sys := f.mock.reqs[0].Messages[0].Content
	for _, name := range []string{"main", "peer"} {
		if strings.Contains(sys, name+" (Tier") {
			t.Errorf("呼べない相手 %q が指示文に載っています", name)
		}
	}
}
