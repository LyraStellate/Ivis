package discord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func line(key, body string, reply bool) msg {
	return msg{key: key, reply: reply, p: Payload{Content: body}}
}

func TestBoardPostsInOrderAndRepliesOnce(t *testing.T) {
	api := &fakeAPI{}
	b := newBoard(api, "ch", "src")

	err := b.flush(context.Background(), at(0), []msg{
		line("status", "-# 実行中", false),
		line("think:0:0", "-# 考えている", true),
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	live := api.live()
	if len(live) != 2 {
		t.Fatalf("%d 通しか出ていない", len(live))
	}
	// 出来事の順がそのまま並びになる。後から差し込む手段は無い。
	if live[0].p.Content != "-# 実行中" {
		t.Fatalf("並びが違う: %q", live[0].p.Content)
	}
	if live[0].reply != "" {
		t.Fatal("状態の表示がリプライになっている")
	}
	if live[1].reply != "src" {
		t.Fatalf("呼びかけへ紐付いていない: %q", live[1].reply)
	}
}

func TestBoardEditsOnlyWhatChanged(t *testing.T) {
	api := &fakeAPI{}
	b := newBoard(api, "ch", "")
	ctx := context.Background()

	first := []msg{line("a", "ひとつめ", false), line("b", "ふたつめ", false)}
	if err := b.flush(ctx, at(0), first, true); err != nil {
		t.Fatal(err)
	}
	changed := []msg{line("a", "ひとつめ", false), line("b", "ふたつめ・追記", false)}
	if err := b.flush(ctx, at(10), changed, true); err != nil {
		t.Fatal(err)
	}

	all := api.all()
	if all[0].edits != 0 {
		t.Fatalf("変わっていないものを %d 回書き換えた", all[0].edits)
	}
	if all[1].edits != 1 {
		t.Fatalf("変わったものの書き換えが %d 回", all[1].edits)
	}
}

func TestBoardHoldsWithinInterval(t *testing.T) {
	api := &fakeAPI{}
	b := newBoard(api, "ch", "")
	ctx := context.Background()
	now := at(0)

	_ = b.flush(ctx, now, []msg{line("a", "1", false)}, true)
	// 間隔の内側で何度呼ばれても 1 回にまとまる。届くたびに送ると
	// レート制限に当たる。
	for i := 1; i <= 5; i++ {
		_ = b.flush(ctx, now.Add(time.Duration(i)*200*time.Millisecond),
			[]msg{line("a", strings.Repeat("1", i+1), false)}, false)
	}
	if n := api.all()[0].edits; n != 0 {
		t.Fatalf("間隔を待たずに %d 回書き換えた", n)
	}

	_ = b.flush(ctx, now.Add(2*time.Second), []msg{line("a", "おわり", false)}, false)
	if n := api.all()[0].edits; n != 1 {
		t.Fatalf("間隔の後に送っていない: %d 回", n)
	}
}

func TestBoardDeletesWhatDisappears(t *testing.T) {
	api := &fakeAPI{}
	b := newBoard(api, "ch", "")
	ctx := context.Background()

	// 推論が伸びて 3 通になり、畳んで 1 通へ戻る形。
	grown := []msg{
		line("status", "-# 実行中", false),
		line("think:0:0", "-# 前半", false),
		line("think:0:1", "-# 中盤", false),
		line("think:0:2", "-# 後半", false),
	}
	if err := b.flush(ctx, at(0), grown, true); err != nil {
		t.Fatal(err)
	}

	collapsed := []msg{line("think:0:0", "-# 推論: 12 秒", false), line("answer:1:0", "答え", false)}
	if err := b.flush(ctx, at(20), collapsed, true); err != nil {
		t.Fatal(err)
	}

	live := api.live()
	got := make([]string, 0, len(live))
	for _, m := range live {
		got = append(got, m.p.Content)
	}
	// 畳んだはずの続きが下に残っていてはいけない。走っている間だけの
	// 表示も消える。
	want := "-# 推論: 12 秒,答え"
	if strings.Join(got, ",") != want {
		t.Fatalf("残っているもの: %v (want %s)", got, want)
	}
}

func TestBoardBacksOffOnRateLimit(t *testing.T) {
	api := &fakeAPI{}
	b := newBoard(api, "ch", "")
	ctx := context.Background()

	_ = b.flush(ctx, at(0), []msg{line("a", "1", false)}, true)
	api.fail = &RateLimited{RetryAfter: 3 * time.Second}
	if err := b.flush(ctx, at(2), []msg{line("a", "12", false)}, true); err == nil {
		t.Fatal("断りが返っていない")
	}
	if b.interval <= baseInterval {
		t.Fatalf("間隔が伸びていない: %v", b.interval)
	}
	// 断られた分は待つ。待たずに送り直すと、断られ続ける。
	if want := at(2).Add(3 * time.Second); b.next.Before(want) {
		t.Fatalf("次に送る時刻が早すぎる: %v (>= %v のはず)", b.next, want)
	}

	// 落ち着けば元へ戻る。伸びたままだと、以後ずっと表示が遅れる。
	for i := 0; i < calmAfter*2; i++ {
		_ = b.flush(ctx, at(100+i*10), []msg{line("a", strings.Repeat("1", i+2), false)}, true)
	}
	if b.interval != baseInterval {
		t.Fatalf("間隔が戻っていない: %v", b.interval)
	}
}

func TestBoardKeepsPostingWhenDeleteFails(t *testing.T) {
	api := &fakeAPI{}
	b := newBoard(api, "ch", "")
	ctx := context.Background()

	if err := b.flush(ctx, at(0), []msg{line("status", "-# 実行中", false)}, true); err != nil {
		t.Fatal(err)
	}

	// 権限が足りずに消せないことがある。それで回答が出なくなるのは
	// 釣り合わない。
	api.fail = errors.New("消せません")
	_ = b.flush(ctx, at(10), []msg{line("answer:0:0", "答えです", false)}, true)

	var found bool
	for _, m := range api.live() {
		if m.p.Content == "答えです" {
			found = true
		}
	}
	if !found {
		t.Fatalf("消せなかったせいで回答が出ていない: %#v", api.live())
	}
}
