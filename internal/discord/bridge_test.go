package discord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/store"
)

type harness struct {
	b     *Bridge
	api   *fakeAPI
	st    *store.Store
	runs  *engine.Runs
	asked []string
	run   func(ctx context.Context, sessionID, text string, emit engine.Emit) error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()

	agentDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "general.json"),
		[]byte(`{"name":"総合","model":"m","instructions":"i","tools":["*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	agents := agent.NewSet()
	agents.Load([]string{agentDir})

	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Default()
	cfg.WorkspaceDir = filepath.Join(dir, "ws")
	cfg.DefaultAgent = "general"

	h := &harness{api: &fakeAPI{name: "general"}, st: st, runs: engine.NewRuns()}
	h.run = func(_ context.Context, _, text string, _ engine.Emit) error {
		h.asked = append(h.asked, text)
		return nil
	}
	h.b = New(Deps{
		Cfg: cfg, Store: st, Agents: agents, Runs: h.runs,
		Run: func(ctx context.Context, id, text string, emit engine.Emit) error {
			return h.run(ctx, id, text, emit)
		},
		Now: time.Now,
	})
	h.b.self = "bot"
	return h
}

func mention(text string) incoming {
	return incoming{ChannelID: "c1", MessageID: "m1", AuthorID: "u1",
		AuthorName: "太郎", Content: "<@bot> " + text}
}

func TestStripRemovesMention(t *testing.T) {
	for _, in := range []string{"<@bot> 調べて", "<@!bot>  調べて", "調べて <@bot>"} {
		if got := strip(in, "bot"); got != "調べて" {
			t.Fatalf("%q から %q が残った", in, got)
		}
	}
}

func TestServeCreatesOneSessionPerChannel(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.b.serve(ctx, h.api, mention("ひとつめ"))
	h.b.serve(ctx, h.api, mention("ふたつめ"))

	first, err := h.st.SessionByChannel(ctx, "c1")
	if err != nil {
		t.Fatalf("チャンネルから会話を引けない: %v", err)
	}
	if first.Source != store.SourceDiscord {
		t.Fatalf("出自が %q", first.Source)
	}
	// 表題はチャンネル名。場所に結び付いた会話なので、最初の依頼で
	// 上書きされては困る。
	if first.Title != "#general" {
		t.Fatalf("表題が %q", first.Title)
	}

	other := mention("べつの場所")
	other.ChannelID = "c2"
	h.b.serve(ctx, h.api, other)
	second, err := h.st.SessionByChannel(ctx, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("別のチャンネルが同じ会話になっている")
	}

	list, _ := h.st.ListSessions(ctx)
	if len(list) != 2 {
		t.Fatalf("会話が %d 件できている", len(list))
	}
}

func TestServePrefixesSpeaker(t *testing.T) {
	h := newHarness(t)
	h.b.serve(context.Background(), h.api, mention("調べて"))

	if len(h.asked) != 1 {
		t.Fatalf("実行が %d 回", len(h.asked))
	}
	// チャンネルには複数の人が居る。誰の発言かが無いと会話が成り立たない。
	if h.asked[0] != "太郎: 調べて" {
		t.Fatalf("渡した本文が %q", h.asked[0])
	}
}

func TestServeRefusesWhileBusy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.b.serve(ctx, h.api, mention("ひとつめ"))

	sess, err := h.st.SessionByChannel(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	// 走っている状態を作る。Web からの送信中でも同じことが起きる。
	h.runs.Begin(sess.ID, func() {})
	defer h.runs.End(sess.ID)

	before := len(h.asked)
	h.b.serve(ctx, h.api, mention("ふたつめ"))
	if len(h.asked) != before {
		t.Fatal("走っている会話で二重に実行した")
	}
	live := h.api.live()
	last := live[len(live)-1]
	if !strings.Contains(last.p.Content, "別の依頼") {
		t.Fatalf("断りが伝わっていない: %q", last.p.Content)
	}
}

func TestServeIgnoresBareMention(t *testing.T) {
	h := newHarness(t)
	in := mention("")
	h.b.serve(context.Background(), h.api, in)

	if len(h.asked) != 0 {
		t.Fatal("中身の無いメンションで実行した")
	}
	live := h.api.live()
	if len(live) != 1 || !strings.Contains(live[0].p.Content, "続けて") {
		t.Fatalf("案内していない: %#v", live)
	}
	if _, err := h.st.SessionByChannel(context.Background(), "c1"); err == nil {
		t.Fatal("中身が無いのに会話を作っている")
	}
}

func TestServeShowsAnswerAndSummary(t *testing.T) {
	h := newHarness(t)
	h.run = func(_ context.Context, _, _ string, emit engine.Emit) error {
		emit(engine.Event{Type: engine.EvtToolCall, Tool: "read_file", ToolCallID: "a",
			Args: map[string]any{"path": "go.mod"}})
		emit(engine.Event{Type: engine.EvtToolResult, ToolCallID: "a", Result: "module ..."})
		emit(engine.Event{Type: engine.EvtDelta, Text: "go.mod を読みました"})
		return nil
	}
	h.b.serve(context.Background(), h.api, mention("読んで"))

	live := h.api.live()
	if len(live) == 0 {
		t.Fatal("何も送っていない")
	}
	// 経過も回答も 1 通に収まり、時間順に並ぶ。
	if len(live) != 1 {
		t.Fatalf("%d 通に分かれている", len(live))
	}
	got := live[0].p.Content
	tool := strings.Index(got, "`read_file : ")
	answer := strings.Index(got, "go.mod を読みました")
	if tool < 0 || answer < 0 || tool > answer {
		t.Fatalf("経過と回答の並びが違う:%s", got)
	}
	// 最後の姿に生成中の印が残っていてはいけない。
	if strings.Contains(got, "▍") {
		t.Fatalf("生成中の印が残っている: %q", got)
	}
}

func TestServeShowsTyping(t *testing.T) {
	h := newHarness(t)
	h.run = func(ctx context.Context, _, _ string, _ engine.Emit) error {
		time.Sleep(30 * time.Millisecond)
		return nil
	}
	h.b.serve(context.Background(), h.api, mention("待って"))
	if h.api.typedCount() == 0 {
		t.Fatal("入力中を出していない")
	}
}

func TestServeReportsFailure(t *testing.T) {
	h := newHarness(t)
	h.run = func(_ context.Context, _, _ string, emit engine.Emit) error {
		emit(engine.Event{Type: engine.EvtError, Error: "繋がりません", Kind: "provider_unavailable"})
		return errors.New("失敗しました")
	}
	h.b.serve(context.Background(), h.api, mention("やって"))

	live := h.api.live()
	last := live[len(live)-1]
	if !strings.Contains(last.p.Content, "繋がりません") {
		t.Fatalf("失敗が出ていない: %q", last.p.Content)
	}
}
