package tools

import "testing"

// 登録されているツールと、それぞれの承認の既定を一覧する。増やしたときに
// 既定を決め忘れていないかを、目で確かめられるようにしておく。
func TestRegistryContents(t *testing.T) {
	r := NewRegistry()
	for _, n := range r.Names() {
		tool, _ := r.Get(n)
		t.Logf("%-18s 承認=%v", n, tool.NeedsApproval())
	}
	if len(r.Names()) != 14 {
		t.Errorf("ツール数 = %d, want 14", len(r.Names()))
	}
}
