package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// 委譲は子の成果だけを親へ返す。子の途中経過は履歴に残るが、親の文脈には入らない。
func TestDelegateReturnsOnlyChildResult(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		switch n {
		case 0: // 親: 委譲する
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": "child", "task": "数えて",
			})}
		case 1: // 子: 途中でツールを使う
			return []provider.Event{text("調べます"), callTool("list_dir", map[string]any{"path": "."})}
		case 2: // 子: 結論
			return []provider.Event{text("0 件でした"), {Type: provider.EventDone}}
		default: // 親: 受け取って締める
			return []provider.Event{text("子によると 0 件です"), {Type: provider.EventDone}}
		}
	})
	id := f.newSession(t, "main")

	if err := f.eng.Run(context.Background(), id, "頼む", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 親の会話には子の途中経過が混ざらない。
	top, err := f.store.ConversationMessages(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range top {
		if strings.Contains(m.Content, "調べます") {
			t.Fatal("子の途中経過が親の会話に混ざっています")
		}
	}

	// 子の発言は親メッセージにぶら下がって保存されている。
	all, _ := f.store.ListMessages(context.Background(), id)
	var nested int
	for _, m := range all {
		if m.ParentID != "" {
			nested++
		}
	}
	if nested == 0 {
		t.Fatal("子の発言が履歴に残っていません")
	}

	// 親が受け取ったのは子の最終出力だけ。
	var toolResult string
	for _, m := range top {
		if m.Role == provider.RoleTool && m.ToolName == "delegate" {
			toolResult = m.Content
		}
	}
	if toolResult != "0 件でした" {
		t.Errorf("親が受け取った成果 = %q, want %q", toolResult, "0 件でした")
	}

	types := strings.Join(f.typesOf(), ",")
	if !strings.Contains(types, EvtDelegateStart) || !strings.Contains(types, EvtDelegateEnd) {
		t.Errorf("委譲のイベントが流れていません: %s", types)
	}
}

// 定義に列挙されていない相手へは委譲できない。
func TestDelegateRejectsUnlistedAgent(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": "main", "task": "自分を呼ぶ",
			})}
		}
		return []provider.Event{text("やめます"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "頼む", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, _ := f.store.ListMessages(context.Background(), id)
	var result string
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			result = m.Content
		}
	}
	if !strings.Contains(result, "許可されていません") {
		t.Errorf("未許可の委譲が拒否されていません: %q", result)
	}
}

// 深さの上限に達したら委譲を断る。
func TestDelegateDepthLimit(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": "child", "task": "任せる",
			})}
		}
		return []provider.Event{text("自分でやります"), {Type: provider.EventDone}}
	})
	f.cfg.MaxDelegationDepth = 0
	id := f.newSession(t, "main")

	if err := f.eng.Run(context.Background(), id, "頼む", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, _ := f.store.ListMessages(context.Background(), id)
	var result string
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			result = m.Content
		}
	}
	if !strings.Contains(result, "深さ") {
		t.Errorf("深さの上限が効いていません: %q", result)
	}
	for _, m := range msgs {
		if m.ParentID != "" {
			t.Fatal("上限を超えて子が動いています")
		}
	}
}

// 委譲が循環したら実行時に検出して打ち切る。
func TestDelegateCycleDetected(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		switch n {
		case 0: // main が loop へ
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": "loop", "task": "戻ってきて",
			})}
		case 1: // loop が main へ戻そうとする
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": "main", "task": "戻る",
			})}
		case 2: // loop が断念
			return []provider.Event{text("戻れません"), {Type: provider.EventDone}}
		default:
			return []provider.Event{text("了解"), {Type: provider.EventDone}}
		}
	})
	id := f.newSession(t, "main")

	if err := f.eng.Run(context.Background(), id, "頼む", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, _ := f.store.ListMessages(context.Background(), id)
	var found bool
	for _, m := range msgs {
		if m.Role == provider.RoleTool && strings.Contains(m.Content, "循環") {
			found = true
		}
	}
	if !found {
		t.Fatal("循環が検出されていません")
	}
}

// 承認が拒否された事実はモデルへ返す。黙って何もしないと、モデルは実行された
// ものとして続きを進めてしまう。
func TestDeniedApprovalIsReportedToModel(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{callTool("write_file", map[string]any{
				"path": "out.txt", "content": "hello",
			})}
		}
		return []provider.Event{text("では書きません"), {Type: provider.EventDone}}
	})
	f.cfg.RequireApproval = true
	f.eng.Approver = denyingApprover{}

	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "書いて", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, _ := f.store.ListMessages(context.Background(), id)
	var result string
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			result = m.Content
		}
	}
	if !strings.Contains(result, "拒否") {
		t.Errorf("拒否がモデルへ伝わっていません: %q", result)
	}
}

type denyingApprover struct{}

func (denyingApprover) Request(ctx context.Context, req ApprovalRequest) (bool, error) {
	return false, nil
}
