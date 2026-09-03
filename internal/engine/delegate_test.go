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

// 自分より上位のエージェントは呼べない。誰を呼べるかは Tier だけで決まる。
func TestDelegateRejectsUpwardTier(t *testing.T) {
	result := delegateOnce(t, "child", "main")
	if !strings.Contains(result, "下位ではない") {
		t.Errorf("上位への委譲が拒否されていません: %q", result)
	}
}

// 同じ Tier どうしも呼べない。呼べてしまうと、同位のあいだで循環しうる。
func TestDelegateRejectsSameTier(t *testing.T) {
	result := delegateOnce(t, "child", "peer")
	if !strings.Contains(result, "下位ではない") {
		t.Errorf("同位への委譲が拒否されていません: %q", result)
	}
}

// 自分自身へは委譲できない。
func TestDelegateRejectsSelf(t *testing.T) {
	result := delegateOnce(t, "main", "main")
	if !strings.Contains(result, "自分自身") {
		t.Errorf("自分自身への委譲が拒否されていません: %q", result)
	}
}

// delegateOnce は from が to へ 1 度だけ委譲を試み、ツールの結果を返す。
func delegateOnce(t *testing.T, from, to string) string {
	t.Helper()
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		if n == 0 {
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": to, "task": "頼む",
			})}
		}
		return []provider.Event{text("やめます"), {Type: provider.EventDone}}
	})
	id := f.newSession(t, from)
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
	return result
}

// 深さの上限に達したら委譲を断る。Tier が単調に増える以上、無限には深く
// ならないが、Tier を大きく飛ばせば依然として深くなりうる。
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

// 記憶を有効にした子は、同じセッションでの前回のやり取りを引き継ぐ。
func TestDelegateMemoryCarriesOver(t *testing.T) {
	f := twoRounds(t, "remember")
	// 4 番目の要求が 2 回目の子の生成。前回の依頼と応答が入っている。
	req := f.mock.reqs[3]
	var joined strings.Builder
	for _, m := range req.Messages {
		joined.WriteString(m.Content)
		joined.WriteString("|")
	}
	got := joined.String()
	if !strings.Contains(got, "1 回目") || !strings.Contains(got, "覚えました") {
		t.Errorf("前回のやり取りが引き継がれていません: %q", got)
	}
}

// 記憶を切った子は毎回まっさらな文脈で始まる。
func TestDelegateWithoutMemoryStartsFresh(t *testing.T) {
	f := twoRounds(t, "child")
	req := f.mock.reqs[3]
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "1 回目") || strings.Contains(m.Content, "覚えました") {
			t.Errorf("引き継がないはずの文脈が入っています: %q", m.Content)
		}
	}
}

// twoRounds は同じ子へ 2 度続けて委譲する 1 ターンを走らせる。
func twoRounds(t *testing.T, child string) *fixture {
	t.Helper()
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		switch n {
		case 0:
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": child, "task": "1 回目",
			})}
		case 1:
			return []provider.Event{text("覚えました"), {Type: provider.EventDone}}
		case 2:
			return []provider.Event{callTool("delegate", map[string]any{
				"agent": child, "task": "2 回目",
			})}
		case 3:
			return []provider.Event{text("2 回目です"), {Type: provider.EventDone}}
		default:
			return []provider.Event{text("終わり"), {Type: provider.EventDone}}
		}
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "頼む", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.mock.reqs) < 4 {
		t.Fatalf("生成の回数が足りません: %d", len(f.mock.reqs))
	}
	return f
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
