package team

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/store"
)

func writeAgent(t *testing.T, dir, id string, a map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func def(tier int) map[string]any {
	return map[string]any{"model": "m", "tier": tier, "instructions": "x"}
}

// setup は共通 2 体・固有 1 体のチームを組む。
func setup(t *testing.T) (*config.Config, *agent.Set, *store.Session) {
	t.Helper()
	root := t.TempDir()
	common := filepath.Join(root, "agents")
	writeAgent(t, common, "boss", def(1))
	writeAgent(t, common, "hand", def(2))

	set := agent.NewSet()
	set.Load([]string{common})

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.AgentPaths = []string{common}

	sess := &store.Session{ID: "s1", AgentID: "boss", Kind: config.KindTeam,
		Members: []string{"boss", "hand"}}
	writeAgent(t, cfg.SessionAgentsDir(sess.ID), "scout", def(2))
	return cfg, set, sess
}

func TestParseMention(t *testing.T) {
	cases := []struct{ in, to, body string }{
		{"@coder これを直して", "coder", "これを直して"},
		{"@* 誰か見て", "*", "誰か見て"},
		{"宛先なし", "", "宛先なし"},
		{"@coder", "coder", ""},
		{"@coder、頼む", "coder", "、頼む"},
		// メールアドレスのような書き出しは無いが、@ だけで始まる文は宛先に
		// ならない。空の宛先を作ると、行き先の無いメッセージができる。
		{"@ 空", "", "@ 空"},
	}
	for _, c := range cases {
		to, body := ParseMention(c.in)
		if to != c.to || body != c.body {
			t.Errorf("ParseMention(%q) = (%q, %q), want (%q, %q)", c.in, to, body, c.to, c.body)
		}
	}
}

// 名簿は共通と固有をひとつに束ねる。並びは Tier 順。
func TestRosterMergesCommonAndLocal(t *testing.T) {
	cfg, set, sess := setup(t)
	r := Load(cfg, set, sess)

	got := r.IDs()
	want := []string{"boss", "hand", "scout"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("名簿 = %v, want %v", got, want)
	}
	m, _ := r.Get("scout")
	if !m.Local {
		t.Error("scout は固有のはずだが、共通として載っている")
	}
	if m, _ := r.Get("boss"); m.Local {
		t.Error("boss は共通のはずだが、固有として載っている")
	}
	if len(r.Errors) != 0 {
		t.Errorf("読み込みの失敗が出ている: %v", r.Errors)
	}
}

// 同じ名前が 2 つあると、宛先が誰を指すのか決められない。固有を残し、
// 衝突は失敗として記録する。黙って片方を消さない。
func TestLocalWinsOnCollision(t *testing.T) {
	cfg, set, sess := setup(t)
	writeAgent(t, cfg.SessionAgentsDir(sess.ID), "hand", def(3))

	r := Load(cfg, set, sess)
	m, ok := r.Get("hand")
	if !ok || !m.Local {
		t.Fatal("固有の hand が名簿に載っていない")
	}
	if m.Tier != 3 {
		t.Errorf("Tier = %d, want 3 (固有の定義)", m.Tier)
	}
	if len(r.Errors) == 0 {
		t.Error("衝突が失敗として記録されていない")
	}
}

// 参加している共通の定義が消えていたら、その 1 体だけが落ちる。
func TestMissingCommonMemberIsReported(t *testing.T) {
	cfg, set, sess := setup(t)
	sess.Members = append(sess.Members, "gone")

	r := Load(cfg, set, sess)
	if _, ok := r.Get("gone"); ok {
		t.Error("定義の無いメンバーが名簿に載っている")
	}
	if len(r.Members) != 3 {
		t.Errorf("メンバー数 = %d, want 3", len(r.Members))
	}
	if len(r.Errors) != 1 {
		t.Errorf("失敗の件数 = %d, want 1", len(r.Errors))
	}
}

// 何を送れるかは Tier の上下だけで決まる。
func TestRelationFollowsTier(t *testing.T) {
	cfg, set, sess := setup(t)
	r := Load(cfg, set, sess)

	cases := []struct{ from, to, want string }{
		{"boss", "hand", RelOrder},    // 下位へは指示
		{"hand", "boss", RelReport},   // 上位へは報告
		{"hand", "scout", RelRequest}, // 同位へは依頼
		{"boss", "居ない", ""},
	}
	for _, c := range cases {
		if got := r.Relation(c.from, c.to); got != c.want {
			t.Errorf("Relation(%s, %s) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

// 指示文の名簿には、相手ごとに何ができるかが言葉で書かれている。Tier の数を
// 出して推論させると、必ずどこかで向きが逆になる。
func TestRollNamesWhatEachCanDo(t *testing.T) {
	cfg, set, sess := setup(t)
	r := Load(cfg, set, sess)

	got := r.Roll("hand")
	for _, want := range []string{"boss", RelReport, "scout", RelRequest, "窓口は boss"} {
		if !strings.Contains(got, want) {
			t.Errorf("名簿に %q が無い:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hand (Tier 2, hand): 報告") {
		t.Error("自分自身に関係が書かれている")
	}
}

// 窓口だけが宛先を委ねられる。誰でも使えると、決めないまま回し続ける。
func TestGuideMentionsAnyoneOnlyForLead(t *testing.T) {
	cfg, set, sess := setup(t)
	r := Load(cfg, set, sess)

	if strings.Contains(Guide(r, "hand"), Anyone) {
		t.Error("窓口でない相手に \"*\" の使い方を教えている")
	}
	if !strings.Contains(Guide(r, "boss"), Anyone) {
		t.Error("窓口に \"*\" の使い方が伝わっていない")
	}
}

// 下位を持つ相手には、上司としての進め方を渡す。全員に同じ文を渡すと、
// 指示を出す側まで「勝手に始めない」「確認を取る」と読み、部下に許可を
// 求め始める。
func TestGuideDependsOnPosition(t *testing.T) {
	cfg, set, sess := setup(t)
	r := Load(cfg, set, sess)

	// boss (Tier 1) の下には hand と scout (Tier 2) が居る。
	boss := Guide(r, "boss")
	for _, want := range []string{"2 人の下位", "上司", "許可や確認を求めない", "指示は依頼ではありません"} {
		if !strings.Contains(boss, want) {
			t.Errorf("上司向けの進め方に %q が無い:\n%s", want, boss)
		}
	}
	if strings.Contains(boss, "勝手に始めない") {
		t.Error("下位を持つ相手に、部下向けの心得を渡している")
	}

	// hand (Tier 2) の下には誰も居ない。
	hand := Guide(r, "hand")
	if !strings.Contains(hand, "下位は居ません") {
		t.Errorf("部下向けの進め方になっていない:\n%s", hand)
	}
	if strings.Contains(hand, "上司です") {
		t.Error("下位の居ない相手を上司として扱っている")
	}
	if !strings.Contains(hand, "窓口は boss") {
		t.Error("全体の判断がどこにあるかが伝わっていない")
	}
}

func TestSubordinates(t *testing.T) {
	cfg, set, sess := setup(t)
	r := Load(cfg, set, sess)

	var got []string
	for _, m := range r.Subordinates("boss") {
		got = append(got, m.ID)
	}
	if strings.Join(got, ",") != "hand,scout" {
		t.Errorf("下位 = %v", got)
	}
	if n := len(r.Subordinates("hand")); n != 0 {
		t.Errorf("同位を下位として数えている: %d", n)
	}
}
