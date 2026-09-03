package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Tier は省略できる。書かれていなければ規定より 1 つ下として扱う。
// 廃止した delegates が残っていても読み込みは通す。手元の定義が一斉に
// 読めなくなる事態を避けるため。
func TestLoadAcceptsOldDefinitions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "legacy.json", `{"model":"m","delegates":["a","b"]}`)

	s := NewSet()
	s.Load([]string{dir})
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("読み込みに失敗しました: %v", errs)
	}
	a, ok := s.Get("legacy")
	if !ok {
		t.Fatal("legacy が読めていません")
	}
	if a.Tier != MinUserTier {
		t.Errorf("Tier = %d, want %d", a.Tier, MinUserTier)
	}
	if a.Name != "legacy" {
		t.Errorf("Name = %q, want ID と同じ", a.Name)
	}
}

// 規定エージェントの Tier はファイルに何が書いてあっても 0 とする。
// 入口が 2 つある状態を、定義を手で書き換えて作れないようにするため。
func TestDefaultAgentTierIsPinned(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, DefaultID+".json", `{"model":"m","tier":7}`)

	s := NewSet()
	s.Load([]string{dir})
	a, ok := s.Get(DefaultID)
	if !ok {
		t.Fatal("規定エージェントが読めていません")
	}
	if a.Tier != 0 {
		t.Errorf("Tier = %d, want 0", a.Tier)
	}
	if !a.Fixed {
		t.Error("規定エージェントに固定の印が付いていません")
	}
}

// 委譲できるのは Tier が真に大きい相手だけ。
func TestCanDelegateTo(t *testing.T) {
	top := &Agent{ID: "top", Tier: 0}
	mid := &Agent{ID: "mid", Tier: 1}
	other := &Agent{ID: "other", Tier: 1}

	cases := []struct {
		from, to *Agent
		want     bool
	}{
		{top, mid, true},
		{mid, top, false},
		{mid, other, false},
		{mid, mid, false},
	}
	for _, c := range cases {
		if got := c.from.CanDelegateTo(c.to); got != c.want {
			t.Errorf("%s -> %s = %v, want %v", c.from.ID, c.to.ID, got, c.want)
		}
	}
}

// Below は自分より下位だけを Tier 順で返す。
func TestBelow(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.json", `{"model":"m","tier":1}`)
	write(t, dir, "c.json", `{"model":"m","tier":3}`)
	write(t, dir, "b.json", `{"model":"m","tier":2}`)

	s := NewSet()
	s.Load([]string{dir})
	a, _ := s.Get("a")
	var ids []string
	for _, x := range s.Below(a) {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, ",") != "b,c" {
		t.Errorf("Below = %v, want [b c]", ids)
	}
}
