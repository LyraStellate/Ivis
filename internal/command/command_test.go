package command

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/store"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		name string
		rest string
		ok   bool
	}{
		{"/stop", "stop", "", true},
		// 打った本人にはどちらを打ったか見えている。大小で別のものにすると、
		// 効かない理由が分からない。
		{"/Stop", "stop", "", true},
		{"/CLEAR", "clear", "", true},
		{"/clear いま", "clear", "いま", true},
		{"/compact ファイル名だけ残して", "compact", "ファイル名だけ残して", true},
		{"こんにちは", "", "", false},
		{"", "", "", false},
		{"/", "", "", false},
		{"/ stop", "", "", false},
		// 途中の / は文の一部である。
		{"a/b について", "", "", false},
	}
	for _, c := range cases {
		name, rest, ok := Parse(c.in)
		if ok != c.ok || name != c.name || rest != c.rest {
			t.Errorf("Parse(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, name, rest, ok, c.name, c.rest, c.ok)
		}
	}
}

// 打ち間違えたときに、何が使えるのかをその場で見せる。
func TestUnknownCommandShowsTheList(t *testing.T) {
	got := Run(context.Background(), &Deps{}, nil, "stoppp", "")
	if !strings.Contains(got, "知らないコマンド") {
		t.Fatalf("返答が %q", got)
	}
	for _, want := range []string{"/stop", "/compact", "/clear", "/help"} {
		if !strings.Contains(got, want) {
			t.Errorf("一覧に %s が無い:\n%s", want, got)
		}
	}
}

// 何も無い場所で「消しました」と返っては、何が起きたのか分からない。
func TestSessionlessCommandsAreRefused(t *testing.T) {
	for _, name := range []string{"stop", "clear", "compact"} {
		got := Run(context.Background(), &Deps{}, nil, name, "")
		if !strings.Contains(got, "まだ会話がありません") {
			t.Errorf("/%s の返答が %q", name, got)
		}
	}
}

// 一覧は会話が無くても出る。使い方を知るのに会話は要らない。
func TestHelpNeedsNoSession(t *testing.T) {
	if got := Run(context.Background(), &Deps{}, nil, "help", ""); !strings.Contains(got, "使えるコマンド") {
		t.Fatalf("返答が %q", got)
	}
}

func fixture(t *testing.T) (*Deps, *store.Session) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sess, err := st.CreateSession(context.Background(), "general", "会話")
	if err != nil {
		t.Fatal(err)
	}
	return &Deps{Store: st, Runs: engine.NewRuns()}, sess
}

// 走っている生成の入力を、途中で組み替えることはできない。
func TestCompactRefusedWhileRunning(t *testing.T) {
	d, sess := fixture(t)
	d.Compact = func(context.Context, string, string) (*engine.CompactResult, error) {
		t.Error("走っている最中にまとめている")
		return nil, nil
	}
	d.Runs.Begin(sess.ID, func() {})
	defer d.Runs.End(sess.ID)

	got := Run(context.Background(), d, sess, "compact", "")
	if !strings.Contains(got, "生成中はまとめられません") {
		t.Fatalf("返答が %q", got)
	}
}

// 引数はそのまま指示文として渡る。
func TestCompactPassesTheInstructions(t *testing.T) {
	d, sess := fixture(t)
	var got string
	d.Compact = func(_ context.Context, _, instructions string) (*engine.CompactResult, error) {
		got = instructions
		return &engine.CompactResult{Summarized: 12}, nil
	}

	said := Run(context.Background(), d, sess, "compact", "ファイル名だけ残して")
	if got != "ファイル名だけ残して" {
		t.Fatalf("渡った指示が %q", got)
	}
	if !strings.Contains(said, "12 件") {
		t.Errorf("件数が伝わらない: %q", said)
	}
	// 取り消せる操作であることを言っておかないと、消えたと思われる。
	if !strings.Contains(said, "巻き戻せば") {
		t.Errorf("戻せることが伝わらない: %q", said)
	}
}

// 失敗したことは黙らない。まとめられたと思ったまま続けられると、次のターンで
// また上限に当たる。
func TestCompactReportsFailure(t *testing.T) {
	d, sess := fixture(t)
	d.Compact = func(context.Context, string, string) (*engine.CompactResult, error) {
		return nil, errors.New("モデルが落ちた")
	}
	got := Run(context.Background(), d, sess, "compact", "")
	if !strings.Contains(got, "まとめられませんでした") || !strings.Contains(got, "モデルが落ちた") {
		t.Fatalf("返答が %q", got)
	}
}
