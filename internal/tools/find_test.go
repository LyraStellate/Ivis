package tools

import (
	"context"
	"strings"
	"testing"
)

func fixture(t *testing.T) *ExecContext {
	t.Helper()
	ec := newCtx(t)
	put(t, ec, "main.go", "package main\n\nfunc main() {}\n")
	put(t, ec, "cmd/app/main.go", "package main\n\n// 入口\nfunc main() {}\n")
	put(t, ec, "internal/store/store.go", "package store\n\nvar ErrNotFound = 1\n")
	put(t, ec, "README.md", "# 見出し\n")
	put(t, ec, "node_modules/junk/a.go", "package junk\n")
	put(t, ec, ".git/config", "[core]\n")
	return ec
}

func TestFindFilesByName(t *testing.T) {
	ec := fixture(t)
	out, err := (&findFilesTool{}).Execute(context.Background(), ec, map[string]any{"pattern": "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go", "cmd/app/main.go", "internal/store/store.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出ていません:\n%s", want, out)
		}
	}
	// 生成物と履歴は探しても目当てが出てこないうえ、件数だけが膨らむ。
	for _, bad := range []string{"node_modules", ".git"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q をたどっています:\n%s", bad, out)
		}
	}
}

func TestFindFilesByPath(t *testing.T) {
	ec := fixture(t)
	out, err := (&findFilesTool{}).Execute(context.Background(), ec,
		map[string]any{"pattern": "cmd/**/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cmd/app/main.go") {
		t.Errorf("階層をまたぐ形が当たっていません:\n%s", out)
	}
	if strings.Contains(out, "internal/store") {
		t.Errorf("関係ないものが混ざっています:\n%s", out)
	}
}

func TestFindFilesNoMatch(t *testing.T) {
	ec := fixture(t)
	out, err := (&findFilesTool{}).Execute(context.Background(), ec, map[string]any{"pattern": "*.rs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ありません") {
		t.Errorf("見つからなかったことが伝わっていません: %q", out)
	}
}

func TestSearchText(t *testing.T) {
	ec := fixture(t)
	out, err := (&searchTextTool{}).Execute(context.Background(), ec,
		map[string]any{"pattern": "func main"})
	if err != nil {
		t.Fatal(err)
	}
	// どのファイルの何行目かが分からないと、次に読む場所が決まらない。
	if !strings.Contains(out, "main.go:3:") {
		t.Errorf("ファイルと行番号が出ていません:\n%s", out)
	}
}

func TestSearchTextWithGlob(t *testing.T) {
	ec := fixture(t)
	out, err := (&searchTextTool{}).Execute(context.Background(), ec,
		map[string]any{"pattern": "package", "glob": "*.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ".go:") {
		t.Errorf("glob で絞れていません:\n%s", out)
	}
}

func TestSearchTextRejectsBadPattern(t *testing.T) {
	ec := fixture(t)
	if _, err := (&searchTextTool{}).Execute(context.Background(), ec,
		map[string]any{"pattern": "([a-"}); err == nil {
		t.Fatal("誤った正規表現が通ってしまいました")
	}
}
