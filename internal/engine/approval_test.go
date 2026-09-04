package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// 設定の一覧に載せたツールは、既定で確認を求めるものでも確認せずに実行する。
// ツールが増えるほど確認の回数が増え、自律駆動が成り立たなくなるため。
func TestAutoApproveSkipsConfirmation(t *testing.T) {
	f := writeOnce(t)
	f.cfg.RequireApproval = true
	f.cfg.AutoApprove = []string{"write_file"}
	f.eng.Approver = denyingApprover{}

	result := runWrite(t, f)
	if strings.Contains(result, "拒否") {
		t.Errorf("確認を飛ばせていません: %q", result)
	}
	if !strings.Contains(result, "書き込みました") {
		t.Errorf("実行されていません: %q", result)
	}
}

// 一覧に無いツールは既定のまま。全部が素通りになると、線引きの意味が消える。
func TestAutoApproveLeavesOthers(t *testing.T) {
	f := writeOnce(t)
	f.cfg.RequireApproval = true
	f.cfg.AutoApprove = []string{"delete_file"}
	f.eng.Approver = denyingApprover{}

	if result := runWrite(t, f); !strings.Contains(result, "拒否") {
		t.Errorf("載っていないツールが素通りしています: %q", result)
	}
}

func TestAutoApproveWildcard(t *testing.T) {
	f := writeOnce(t)
	f.cfg.RequireApproval = true
	f.cfg.AutoApprove = []string{"*"}
	f.eng.Approver = denyingApprover{}

	if result := runWrite(t, f); strings.Contains(result, "拒否") {
		t.Errorf("* が効いていません: %q", result)
	}
}

func writeOnce(t *testing.T) *fixture {
	t.Helper()
	return newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{callTool("write_file", map[string]any{
				"path": "out.txt", "content": "hello",
			})}
		}
		return []provider.Event{text("書きました"), {Type: provider.EventDone}}
	})
}

func runWrite(t *testing.T, f *fixture) string {
	t.Helper()
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
	return result
}
