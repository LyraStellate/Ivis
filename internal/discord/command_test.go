package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/store"
)

// 見分け方そのものの試験は internal/command にある。ここで確かめるのは、
// Discord の呼びかけがその表へ正しく届くかである。

// lastSaid は最後に投稿された本文を返す。
func (h *harness) lastSaid(t *testing.T) string {
	t.Helper()
	msgs := h.api.all()
	if len(msgs) == 0 {
		t.Fatal("何も投稿されていない")
	}
	return msgs[len(msgs)-1].p.Content
}

// コマンドはエージェントへ流さない。流してから判断させると、止めたいという
// 依頼が生成の順番待ちに並ぶ。
func TestCommandDoesNotReachTheAgent(t *testing.T) {
	h := newHarness(t)
	h.b.serve(context.Background(), h.api, mention("/help"))

	if len(h.asked) != 0 {
		t.Fatalf("エージェントへ流れている: %v", h.asked)
	}
	said := h.lastSaid(t)
	for _, want := range []string{"/stop", "/clear", "/help"} {
		if !strings.Contains(said, want) {
			t.Errorf("一覧に %s が無い:\n%s", want, said)
		}
	}
}

// 先頭に / が無ければ、それはエージェントへの依頼である。
func TestPlainMentionStillReachesTheAgent(t *testing.T) {
	h := newHarness(t)
	h.b.serve(context.Background(), h.api, mention("こんにちは"))

	if len(h.asked) != 1 {
		t.Fatalf("実行が %d 回", len(h.asked))
	}
	if !strings.Contains(h.asked[0], "こんにちは") {
		t.Fatalf("渡した本文が %q", h.asked[0])
	}
}

// 打ち間違いには、何が使えるのかをその場で見せる。黙って流すと、コマンドの
// つもりで書いたものがそのまま依頼として実行される。
func TestUnknownCommandShowsTheList(t *testing.T) {
	h := newHarness(t)
	h.b.serve(context.Background(), h.api, mention("/stoppp"))

	if len(h.asked) != 0 {
		t.Fatalf("エージェントへ流れている: %v", h.asked)
	}
	said := h.lastSaid(t)
	if !strings.Contains(said, "知らないコマンド") || !strings.Contains(said, "/stop") {
		t.Errorf("返答が %q", said)
	}
}

func TestStopCommandCancelsTheRun(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// 走っている状態を作る。コマンドはチャンネルからしか会話を知れない。
	h.b.serve(ctx, h.api, mention("なにか"))
	sess, err := h.st.SessionByChannel(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	stopped := false
	h.runs.Begin(sess.ID, func() { stopped = true })
	defer h.runs.End(sess.ID)

	// 止めるのは誰にとっても安全なので、使える相手を絞らない。承認と違い、
	// その場に居合わせた人が止められないと緊急停止の役に立たない。
	other := mention("/stop")
	other.AuthorID = "u2"
	h.b.serve(ctx, h.api, other)

	if !stopped {
		t.Fatal("中断が呼ばれていない")
	}
	if said := h.lastSaid(t); !strings.Contains(said, "止めました") {
		t.Errorf("返答が %q", said)
	}
}

// 走っていないときに「止めました」と返っては、何が起きたのか分からない。
func TestStopWithNothingRunning(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.b.serve(ctx, h.api, mention("なにか"))
	h.b.serve(ctx, h.api, mention("/stop"))

	if said := h.lastSaid(t); !strings.Contains(said, "走っているものはありません") {
		t.Errorf("返答が %q", said)
	}
}

// 会話がまだ無い場所で「消しました」と返っては、何が起きたのか分からない。
func TestCommandWithoutSession(t *testing.T) {
	h := newHarness(t)
	h.b.serve(context.Background(), h.api, mention("/clear"))

	if said := h.lastSaid(t); !strings.Contains(said, "まだ会話がありません") {
		t.Errorf("返答が %q", said)
	}
}

func TestClearRemovesTheHistory(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// 実行の代わりは記録するだけなので、履歴は自分で積む。
	h.b.serve(ctx, h.api, mention("ひとつめ"))
	sess, err := h.st.SessionByChannel(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*store.Message{
		{SessionID: sess.ID, Role: "user", Content: "太郎: ひとつめ", AgentID: "general"},
		{SessionID: sess.ID, Role: "assistant", Content: "はい", AgentID: "general"},
	} {
		if err := h.st.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.st.SetContextUsage(ctx, sess.ID, 1200, 16384); err != nil {
		t.Fatal(err)
	}

	h.b.serve(ctx, h.api, mention("/clear"))
	if said := h.lastSaid(t); !strings.Contains(said, "履歴を消しました") {
		t.Fatalf("返答が %q", said)
	}

	msgs, err := h.st.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("履歴が %d 件残っている", len(msgs))
	}

	// 会話そのものは残す。消して作り直すと、チャンネルとの結び付きが
	// 張り直され、作業ディレクトリも別の場所になる。
	again, err := h.st.SessionByChannel(ctx, "c1")
	if err != nil {
		t.Fatalf("会話ごと消えている: %v", err)
	}
	if again.ID != sess.ID {
		t.Fatal("会話が作り直されている")
	}
	// 実測値が無くなったので、使用量は不明へ戻っていること。
	if again.ContextTokens != 0 {
		t.Errorf("コンテキスト使用量が %d のまま", again.ContextTokens)
	}
}

// 走っている最中に履歴を消すと、そのターンが自分の書き込み先を失う。
func TestClearRefusedWhileRunning(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.b.serve(ctx, h.api, mention("なにか"))
	sess, err := h.st.SessionByChannel(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	h.runs.Begin(sess.ID, func() {})
	defer h.runs.End(sess.ID)

	h.b.serve(ctx, h.api, mention("/clear"))
	if said := h.lastSaid(t); !strings.Contains(said, "生成中は消せません") {
		t.Errorf("返答が %q", said)
	}
}
