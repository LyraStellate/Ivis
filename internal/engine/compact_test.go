package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
)

// say は 1 往復 (利用者の発言と応答) を積む。
func say(t *testing.T, f *fixture, sessionID, user, reply string) {
	t.Helper()
	ctx := context.Background()
	for _, m := range []*store.Message{
		{SessionID: sessionID, Role: provider.RoleUser, Content: user, AgentID: "main"},
		{SessionID: sessionID, Role: provider.RoleAssistant, Content: reply, AgentID: "main"},
	} {
		if err := f.store.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
}

// exchanges は n 往復ぶんを積む。
func exchanges(t *testing.T, f *fixture, sessionID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		say(t, f, sessionID, "依頼", "応答")
	}
}

func newSession(t *testing.T, f *fixture) string {
	t.Helper()
	sess, err := f.store.CreateSession(context.Background(), "main", "圧縮")
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

// 要約を 1 件書き、それ以降だけをモデルへ渡す。元の発言は消さない。
func TestCompactKeepsTheOriginals(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("まとめました"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 8)

	res, err := f.eng.Compact(ctx, id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summarized != 16 {
		t.Fatalf("まとめたのが %d 件。8 往復 = 16 件のはず", res.Summarized)
	}

	all, err := f.store.ListMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// 元の 16 件は消えない。要約が 1 件増えるだけ。
	if len(all) != 17 {
		t.Fatalf("履歴が %d 件。消してはならない", len(all))
	}

	// モデルへ渡す入力は要約だけになる。
	input, err := f.store.ConversationMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 1 || input[0].Role != store.RoleSummary {
		t.Fatalf("入力が %d 件で先頭が %q。要約 1 件のはず", len(input), input[0].Role)
	}

	// 圧縮のあとに続けた分は、そのまま入力へ入る。
	say(t, f, id, "つぎ", "はい")
	input, err = f.store.ConversationMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 3 {
		t.Fatalf("入力が %d 件。要約 1 + 続き 2 のはず", len(input))
	}
}

// 要約は依頼ではなく前提である。利用者の発言として渡すと、モデルはそれを
// 新しい指示として読む。
func TestSummaryGoesInAsContext(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("これまでの経過"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 8)
	if _, err := f.eng.Compact(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}

	history, err := f.store.ConversationMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	msgs := toProviderMessages(history)
	if len(msgs) == 0 || msgs[0].Role != provider.RoleSystem {
		t.Fatalf("要約が %v として渡っている", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "これまでの経過") {
		t.Fatalf("要約の中身が渡っていない: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "要約") {
		t.Fatal("何の文章かが書かれていない。モデルは新しい指示として読む")
	}
}

// 要約しか無い会話をもう一度まとめても、要約の要約ができるだけで何も減らない。
func TestNothingToCompact(t *testing.T) {
	calls := 0
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		calls++
		return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 3)
	if _, err := f.eng.Compact(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}

	if _, err := f.eng.Compact(ctx, id, "", nil); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("生成が %d 回。まとめる分が無いなら呼んではならない", calls)
	}
}

// 中途半端な要約を書いて入力を切り替えるほうが、圧縮しないより悪い。
func TestFailedCompactionWritesNothing(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{{Type: provider.EventError, Err: errors.New("落ちた")}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 8)

	if _, err := f.eng.Compact(ctx, id, "", nil); err == nil {
		t.Fatal("失敗したのに成功として返っている")
	}
	all, err := f.store.ListMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 16 {
		t.Fatalf("履歴が %d 件。何も書いてはならない", len(all))
	}
}

// 引数は既定に足すのではなく置き換える。両方生かすと、相反する指示が並んだ
// ときにどちらが勝つかが決まらない。
func TestInstructionsReplaceTheDefault(t *testing.T) {
	var sys string
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		sys = req.Messages[0].Content
		return []provider.Event{text("はい"), {Type: provider.EventDone}}
	})
	id := newSession(t, f)
	exchanges(t, f, id, 8)

	if _, err := f.eng.Compact(context.Background(), id, "ファイル名だけ残して", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sys, "ファイル名だけ残して") {
		t.Fatalf("指示が渡っていない: %q", sys)
	}
	if strings.Contains(sys, "KEEP:") {
		t.Fatal("既定の指示が残っている。相反する指示が並ぶ")
	}
}

// 要約に道具は要らず、推論の過程は捨てる値である。どちらもコンテキストと
// 時間を使うだけになる。
func TestCompactionAsksForNeitherToolsNorThinking(t *testing.T) {
	var req provider.Request
	f := newFixture(t, func(n int, r provider.Request) []provider.Event {
		req = r
		return []provider.Event{text("はい"), {Type: provider.EventDone}}
	})
	id := newSession(t, f)
	exchanges(t, f, id, 8)
	if _, err := f.eng.Compact(context.Background(), id, "", nil); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 0 {
		t.Errorf("道具を %d 件渡している", len(req.Tools))
	}
	if req.Think {
		t.Error("推論を求めている")
	}
}

// 上限に近づいたら、ターンの終わりに自分でまとめる。
func TestAutoCompactAtTheThreshold(t *testing.T) {
	cases := []struct {
		name   string
		tokens int
		want   bool
	}{
		{"89% では走らない", 8900, false},
		{"90% で走る", 9000, true},
		{"91% で走る", 9100, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ran := false
			f := newFixture(t, func(n int, req provider.Request) []provider.Event {
				ran = true
				return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
			})
			ctx := context.Background()
			id := newSession(t, f)
			exchanges(t, f, id, 8)
			if err := f.store.SetContextUsage(ctx, id, c.tokens, 10000); err != nil {
				t.Fatal(err)
			}

			f.eng.maybeCompact(ctx, id, f.emit)
			if ran != c.want {
				t.Fatalf("圧縮が走った = %v, want %v", ran, c.want)
			}
		})
	}
}

// 圧縮しても割合が下がらないことはある。そこで繰り返すと、生成が終わるたびに
// 生成が走り続ける。
func TestAutoCompactRunsOncePerTurn(t *testing.T) {
	calls := 0
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		calls++
		return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 20)
	if err := f.store.SetContextUsage(ctx, id, 9900, 10000); err != nil {
		t.Fatal(err)
	}

	f.eng.maybeCompact(ctx, id, f.emit)
	if calls != 1 {
		t.Fatalf("生成が %d 回", calls)
	}
}

// 黙って会話の見え方が変わると、応答が変わった理由が読めない。
func TestAutoCompactTellsTheUser(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 8)
	if err := f.store.SetContextUsage(ctx, id, 9500, 10000); err != nil {
		t.Fatal(err)
	}

	f.eng.maybeCompact(ctx, id, f.emit)
	var notices []string
	for _, ev := range f.events {
		if ev.Type == EvtNotice {
			notices = append(notices, ev.Text)
		}
	}
	if len(notices) != 2 {
		t.Fatalf("知らせが %d 件: %v", len(notices), notices)
	}
	if !strings.Contains(notices[1], "まとめました") {
		t.Errorf("結果が伝わらない: %q", notices[1])
	}
}

// 使用量は実測でしか分からない。90% のまま出し続けると、圧縮が効かなかった
// ように見える。
func TestCompactResetsTheUsage(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 8)
	if err := f.store.SetContextUsage(ctx, id, 9500, 10000); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Compact(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}

	sess, err := f.store.GetSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ContextTokens != 0 || sess.ContextLimit != 0 {
		t.Fatalf("使用量が %d/%d のまま", sess.ContextTokens, sess.ContextLimit)
	}
}

// 2 度目の圧縮は、前の要約とそれ以降をまとめて 1 件にする。古い話ほど圧縮が
// 重なって短くなる。
func TestSecondCompactionFoldsTheFirst(t *testing.T) {
	var last string
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		last = req.Messages[1].Content
		return []provider.Event{text("まとめ"), {Type: provider.EventDone}}
	})
	ctx := context.Background()
	id := newSession(t, f)
	exchanges(t, f, id, 8)
	if _, err := f.eng.Compact(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}
	exchanges(t, f, id, 8)
	if _, err := f.eng.Compact(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(last, "これまでの要約") {
		t.Fatalf("前の要約が対象に入っていない:\n%s", last)
	}
	input, err := f.store.ConversationMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if input[0].Role != store.RoleSummary {
		t.Fatal("最後の要約から始まっていない")
	}
	for _, m := range input[1:] {
		if m.Role == store.RoleSummary {
			t.Fatal("古い要約が入力に残っている")
		}
	}
}
