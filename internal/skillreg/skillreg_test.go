package skillreg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "SKILL.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const claudeStyle = `---
name: design-doc
description: 実装前に設計をまとめる。
---

# 本文

ここが本文である。
`

func TestLoadsClaudeStyleSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "design-doc", claudeStyle)

	r := New()
	r.Load([]string{root})

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("スキル数 = %d, want 1 (errors: %v)", len(list), r.Errors())
	}
	if list[0].Name != "design-doc" {
		t.Errorf("name = %q", list[0].Name)
	}
	if !strings.Contains(list[0].Description, "設計") {
		t.Errorf("description = %q", list[0].Description)
	}

	// 本文は一覧に含めない。必要になった時点で読む。
	body, err := r.Body("design-doc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "ここが本文である") {
		t.Errorf("本文が読めていません: %q", body)
	}
	if strings.Contains(body, "description:") {
		t.Error("frontmatter が本文に混ざっています")
	}
}

// frontmatter が欠けていても落とさず、ディレクトリ名で代替する。
func TestSkillWithoutFrontmatterFallsBackToDirName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "bare-skill", "# 見出しだけ\n\n本文。\n")

	r := New()
	r.Load([]string{root})

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("スキル数 = %d, want 1", len(list))
	}
	if list[0].Name != "bare-skill" {
		t.Errorf("name = %q, want bare-skill", list[0].Name)
	}
}

// 先に指定したパスが優先され、衝突は記録される。黙って捨てると、利用者は
// 編集したスキルが反映されない理由に辿り着けない。
func TestConflictPrefersFirstPathAndRecordsIt(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeSkill(t, first, "dup", "---\nname: dup\ndescription: 先\n---\n先の本文\n")
	writeSkill(t, second, "dup", "---\nname: dup\ndescription: 後\n---\n後の本文\n")

	r := New()
	r.Load([]string{first, second})

	if len(r.List()) != 1 {
		t.Fatalf("スキル数 = %d, want 1", len(r.List()))
	}
	body, _ := r.Body("dup")
	if !strings.Contains(body, "先の本文") {
		t.Errorf("先に指定したパスが優先されていません: %q", body)
	}
	conflicts := r.Conflicts()
	if len(conflicts) != 1 || conflicts[0].Name != "dup" {
		t.Fatalf("衝突が記録されていません: %v", conflicts)
	}
	if !strings.Contains(conflicts[0].Shadows, second) {
		t.Errorf("無視した側が記録されていません: %v", conflicts[0])
	}
}

// 1 階層深いところに置かれたスキルも見つける。
func TestFindsNestedSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "plugin"), "inner", claudeStyle)

	r := New()
	r.Load([]string{root})
	if _, ok := r.Get("design-doc"); !ok {
		t.Fatalf("入れ子のスキルが見つかりません: %v", r.List())
	}
}

func TestMissingSearchPathIsNotAnError(t *testing.T) {
	r := New()
	r.Load([]string{filepath.Join(t.TempDir(), "does-not-exist")})
	if len(r.Errors()) != 0 {
		t.Errorf("未作成の探索パスがエラーになっています: %v", r.Errors())
	}
}

func TestFilterRespectsAllowList(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "a", "---\nname: a\ndescription: A\n---\nA\n")
	writeSkill(t, root, "b", "---\nname: b\ndescription: B\n---\nB\n")

	r := New()
	r.Load([]string{root})

	if got := len(r.Filter([]string{"*"})); got != 2 {
		t.Errorf("Filter(*) = %d, want 2", got)
	}
	if got := len(r.Filter([]string{"a"})); got != 1 {
		t.Errorf("Filter(a) = %d, want 1", got)
	}
	if got := len(r.Filter(nil)); got != 0 {
		t.Errorf("Filter(nil) = %d, want 0", got)
	}
}
