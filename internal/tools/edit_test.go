package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newCtx(t *testing.T) *ExecContext {
	t.Helper()
	return &ExecContext{Workspace: t.TempDir(), Confined: true}
}

func put(t *testing.T, ec *ExecContext, name, body string) string {
	t.Helper()
	p := filepath.Join(ec.Workspace, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 一致が 1 か所に定まるときだけ差し替える。定まらないまま書き換えると、
// モデルは直したつもりで別の場所を壊す。
func TestEditFile(t *testing.T) {
	ec := newCtx(t)
	p := put(t, ec, "a.txt", "one\ntwo\nthree\n")
	tool := &editFileTool{}

	out, err := tool.Execute(context.Background(), ec, map[string]any{
		"path": "a.txt", "old": "two", "new": "TWO",
	})
	if err != nil {
		t.Fatalf("差し替えに失敗: %v", err)
	}
	if read(t, p) != "one\nTWO\nthree\n" {
		t.Errorf("中身 = %q", read(t, p))
	}
	if !strings.Contains(out, "1 か所") {
		t.Errorf("結果 = %q", out)
	}
}

func TestEditFileRejectsAmbiguous(t *testing.T) {
	ec := newCtx(t)
	p := put(t, ec, "a.txt", "x\nx\nx\n")
	tool := &editFileTool{}

	_, err := tool.Execute(context.Background(), ec, map[string]any{
		"path": "a.txt", "old": "x", "new": "y",
	})
	if err == nil {
		t.Fatal("複数一致が通ってしまいました")
	}
	// 件数を返さないと、モデルはどれくらい絞ればよいか分からない。
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("件数が示されていません: %v", err)
	}
	if read(t, p) != "x\nx\nx\n" {
		t.Error("断ったのに書き換わっています")
	}
}

func TestEditFileRejectsMissing(t *testing.T) {
	ec := newCtx(t)
	put(t, ec, "a.txt", "one\n")
	_, err := (&editFileTool{}).Execute(context.Background(), ec, map[string]any{
		"path": "a.txt", "old": "zzz", "new": "y",
	})
	if err == nil {
		t.Fatal("一致 0 件が通ってしまいました")
	}
}

// 動かした結果として何かが消えたことに気づけないため、上書きは断る。
func TestMoveFileRefusesOverwrite(t *testing.T) {
	ec := newCtx(t)
	put(t, ec, "a.txt", "A")
	put(t, ec, "b.txt", "B")

	_, err := (&moveFileTool{}).Execute(context.Background(), ec, map[string]any{
		"from": "a.txt", "to": "b.txt",
	})
	if err == nil {
		t.Fatal("上書きが通ってしまいました")
	}
	if read(t, filepath.Join(ec.Workspace, "b.txt")) != "B" {
		t.Error("行き先が壊れています")
	}
}

func TestMoveFileCreatesParent(t *testing.T) {
	ec := newCtx(t)
	put(t, ec, "a.txt", "A")
	if _, err := (&moveFileTool{}).Execute(context.Background(), ec, map[string]any{
		"from": "a.txt", "to": "sub/dir/b.txt",
	}); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(ec.Workspace, "sub", "dir", "b.txt")) != "A" {
		t.Error("動かせていません")
	}
}

// 木が丸ごと消えるのが最も取り返しがつかない。明示的に頼まれたときだけ消す。
func TestDeleteFileNeedsRecursiveForDir(t *testing.T) {
	ec := newCtx(t)
	put(t, ec, "sub/a.txt", "A")
	tool := &deleteFileTool{}

	if _, err := tool.Execute(context.Background(), ec, map[string]any{"path": "sub"}); err == nil {
		t.Fatal("ディレクトリが黙って消えました")
	}
	if _, err := os.Stat(filepath.Join(ec.Workspace, "sub")); err != nil {
		t.Fatal("断ったのに消えています")
	}

	if _, err := tool.Execute(context.Background(), ec, map[string]any{
		"path": "sub", "recursive": true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ec.Workspace, "sub")); err == nil {
		t.Error("recursive を指定しても消えていません")
	}
}

// モデルは真偽値を文字列で返すことがある。
func TestDeleteFileAcceptsStringBool(t *testing.T) {
	ec := newCtx(t)
	put(t, ec, "sub/a.txt", "A")
	if _, err := (&deleteFileTool{}).Execute(context.Background(), ec, map[string]any{
		"path": "sub", "recursive": "true",
	}); err != nil {
		t.Fatal(err)
	}
}
