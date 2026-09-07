package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRejectsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		agent *Agent
		field string
	}{
		{"ID が空", &Agent{Model: "m", Tier: 1}, "id"},
		{"ID に区切り", &Agent{ID: "a/b", Model: "m", Tier: 1}, "id"},
		{"ID が親参照", &Agent{ID: "..", Model: "m", Tier: 1}, "id"},
		{"モデル未指定", &Agent{ID: "a", Tier: 1}, "model"},
		{"利用者が Tier 0", &Agent{ID: "a", Model: "m", Tier: 0}, "tier"},
		{"規定が Tier 1", &Agent{ID: DefaultID, Model: "m", Tier: 1}, "tier"},
		{"知らない色", &Agent{ID: "a", Model: "m", Tier: 1, Color: "puce"}, "color"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.agent)
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate = %v, want FieldError", err)
			}
			if fe.Field != c.field {
				t.Errorf("Field = %q, want %q", fe.Field, c.field)
			}
		})
	}
}

// 保存した内容はそのまま読み直せる。書いたものを自分で読み返せないと、
// 画面が返す一覧とファイルの中身が食い違う。
func TestSaveRoundTrips(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{
		ID: "writer", Name: "Writer", Description: "書く",
		Model: "m", Instructions: "指示", Tier: 2,
		Tools: []string{"read_file"}, Skills: []string{"*"},
		Memory: true, Thinking: true, Color: "amber",
		Options: map[string]any{"temperature": 0.2},
	}
	if err := Save(dir, a); err != nil {
		t.Fatal(err)
	}

	s := NewSet()
	s.Load([]string{dir})
	got, ok := s.Get("writer")
	if !ok {
		t.Fatalf("読み直せません: %v", s.Errors())
	}
	if got.Tier != 2 || !got.Memory || !got.Thinking || got.Color != "amber" {
		t.Errorf("読み直した内容が違います: %+v", got)
	}
	if got.File != filepath.Join(dir, "writer.json") {
		t.Errorf("File = %q", got.File)
	}
}

// 規定エージェントは削除できない。入口が失われるとどのエージェントも呼べない。
func TestDeleteRefusesDefault(t *testing.T) {
	if err := Delete(&Agent{ID: DefaultID, File: "x"}); err == nil {
		t.Fatal("規定エージェントの削除が通ってしまいました")
	}
}

// 何も無ければ雛形一式を置く。規定だけが消えていれば規定だけ作り直す。
func TestBootstrap(t *testing.T) {
	dir := t.TempDir()
	if err := Bootstrap([]string{dir}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{DefaultID + ".json", "researcher.json"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("%s が作られていません", n)
		}
	}

	// 利用者の編集は上書きしない。
	edited := filepath.Join(dir, "researcher.json")
	if err := os.WriteFile(edited, []byte(`{"model":"edited"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, DefaultID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap([]string{dir}); err != nil {
		t.Fatal(err)
	}

	s := NewSet()
	s.Load([]string{dir})
	if _, ok := s.Get(DefaultID); !ok {
		t.Error("規定エージェントが作り直されていません")
	}
	r, ok := s.Get("researcher")
	if !ok || r.Model != "edited" {
		t.Errorf("利用者の編集が上書きされました: %+v", r)
	}
}

// セッション固有の定義は Tier 0 を持てる。共通で 0 を規定エージェントだけに
// 絞っているのは、会話の入口が 2 つある状態を定義の書き換えで作れないように
// するためだった (#528664)。チームの入口は窓口として会話が明示して持つので、
// そこへ同じ制限を掛ける理由がない (#731906)。
func TestLocalDefinitionsMayBeTierZero(t *testing.T) {
	peer := &Agent{ID: "peer", Model: "m", Tier: 0}

	if err := Validate(peer); err == nil {
		t.Error("共通で Tier 0 が通ってしまう")
	}
	if err := ValidateLocal(peer); err != nil {
		t.Errorf("会話の中で Tier 0 が通らない: %v", err)
	}
	if err := ValidateLocal(&Agent{ID: "peer", Model: "m", Tier: -1}); err == nil {
		t.Error("負の Tier が通ってしまう")
	}

	dir := t.TempDir()
	if err := SaveLocal(dir, peer); err != nil {
		t.Fatalf("書き出せない: %v", err)
	}
	found, errs := ReadDir(dir)
	if len(errs) != 0 || len(found) != 1 {
		t.Fatalf("読み直せない: %v %v", found, errs)
	}
	if found[0].Tier != 0 {
		t.Errorf("Tier = %d, want 0", found[0].Tier)
	}
}

// 共通の一覧では、ファイルに 0 と書かれていても規定エージェント以外は
// 1 へ引き上げる。入口を 2 つ作れないようにするため (#528664)。
func TestCommonSetStillForbidsTierZero(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLocal(dir, &Agent{ID: "peer", Model: "m", Tier: 0}); err != nil {
		t.Fatal(err)
	}
	s := NewSet()
	s.Load([]string{dir})
	a, ok := s.Get("peer")
	if !ok {
		t.Fatal("読めていない")
	}
	if a.Tier != MinUserTier {
		t.Errorf("Tier = %d, want %d", a.Tier, MinUserTier)
	}
}
