package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInRootAllowsInside(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{".", "a", "a/b", "a/b/new.txt", "a/../a/b"} {
		got, err := resolveInRoot(root, rel)
		if err != nil {
			t.Errorf("resolveInRoot(%q) = err %v, want 成功", rel, err)
			continue
		}
		if !within(realRoot(t, root), got) {
			t.Errorf("resolveInRoot(%q) = %q が境界の外にあります", rel, got)
		}
	}
}

func TestResolveInRootRejectsEscapes(t *testing.T) {
	root := t.TempDir()

	cases := []string{
		"..",
		"../outside.txt",
		"a/../../outside.txt",
		"a/b/../../../outside.txt",
	}
	for _, rel := range cases {
		if _, err := resolveInRoot(root, rel); err == nil {
			t.Errorf("resolveInRoot(%q) が通ってしまいました", rel)
		}
	}
}

func TestResolveInRootRejectsAbsolute(t *testing.T) {
	root := t.TempDir()

	cases := []string{
		filepath.Join(root, "inside.txt"), // 中を指していても絶対指定は断る
		`C:\Windows\System32\drivers\etc\hosts`,
		`\server\share\file`,
		"/etc/passwd",
	}
	for _, rel := range cases {
		if _, err := resolveInRoot(root, rel); err == nil {
			t.Errorf("絶対指定 %q が通ってしまいました", rel)
		}
	}
}

// 文字列としてのパスで判定すると、リンクを経由した境界外アクセスを見逃す。
func TestResolveInRootFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("秘密"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		// Windows では既定でシンボリックリンクを作れないことがある。
		t.Skipf("シンボリックリンクを作成できません: %v", err)
	}

	if _, err := resolveInRoot(root, "escape/secret.txt"); err == nil {
		t.Fatal("リンク経由で境界の外へ出られてしまいました")
	}
}

func TestResolveInRootRejectsEmpty(t *testing.T) {
	if _, err := resolveInRoot(t.TempDir(), "   "); err == nil {
		t.Fatal("空のパスが通ってしまいました")
	}
}

func TestDisplayPathIsRelative(t *testing.T) {
	root := t.TempDir()
	got := displayPath(root, filepath.Join(root, "a", "b.txt"))
	if strings.Contains(got, root) {
		t.Errorf("displayPath = %q, 絶対パスのままです", got)
	}
	if got != "a/b.txt" {
		t.Errorf("displayPath = %q, want a/b.txt", got)
	}
}

func realRoot(t *testing.T, root string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return r
}
