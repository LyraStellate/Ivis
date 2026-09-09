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

// setup は共通 2 体・チーム 1 体を置き、3 人とも有効にした会話を返す。
func setup(t *testing.T) (*agent.Set, *agent.Set, *store.Session) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.AgentPaths = []string{filepath.Join(root, "agents")}
	writeAgent(t, cfg.AgentPaths[0], "boss", def(1))
	writeAgent(t, cfg.AgentPaths[0], "hand", def(2))
	writeAgent(t, cfg.TeamAgentDir(), "scout", def(2))

	set := agent.NewSet()
	set.Load(cfg.AgentPaths)
	teamSet := agent.NewSet()
	teamSet.LoadTeam(cfg.TeamAgentPaths())

	sess := &store.Session{ID: "s1", AgentID: "boss", Kind: config.KindTeam,
		Members: []string{"boss", "hand", "scout"}}
	return set, teamSet, sess
}

func TestParseMention(t *testing.T) {
	cases := []struct{ in, to, body string }{
		{"@coder これを直して", "coder", "これを直して"},
		{"宛先なし", "", "宛先なし"},
		{"@coder", "coder", ""},
		{"@coder、頼む", "coder", "、頼む"},
		// @ だけで始まる文は宛先にならない。空の宛先を作ると、行き先の無い
		// メッセージができる。
		{"@ 空", "", "@ 空"},
	}
	for _, c := range cases {
		to, body := ParseMention(c.in)
		if to != c.to || body != c.body {
			t.Errorf("ParseMention(%q) = (%q, %q), want (%q, %q)", c.in, to, body, c.to, c.body)
		}
	}
}

// 名簿は「有効にしてある ID」を 2 つの一覧から引いて組み立てる。並びは Tier 順。
func TestRosterDrawsFromBothSets(t *testing.T) {
	set, teamSet, sess := setup(t)
	r := Load(set, teamSet, sess)

	if got := strings.Join(r.IDs(), ","); got != "boss,hand,scout" {
		t.Fatalf("名簿 = %v, want boss,hand,scout", got)
	}
	if m, _ := r.Get("scout"); m.Scope != ScopeTeam {
		t.Errorf("scout の由来 = %q, want %q", m.Scope, ScopeTeam)
	}
	if m, _ := r.Get("boss"); m.Scope != ScopeCommon {
		t.Errorf("boss の由来 = %q, want %q", m.Scope, ScopeCommon)
	}
	if len(r.Errors) != 0 {
		t.Errorf("読み込みの失敗が出ている: %v", r.Errors)
	}
}

// 有効にしていないものは名簿に載らない。定義があることと、この会話で使う
// ことは別である。
func TestOnlyEnabledAgentsAreMembers(t *testing.T) {
	set, teamSet, sess := setup(t)
	sess.Members = []string{"hand"}

	r := Load(set, teamSet, sess)
	if got := strings.Join(r.IDs(), ","); got != "hand" {
		t.Errorf("名簿 = %v, want hand だけ", got)
	}
}

// 定義が消えた相手は名簿から落ちる。黙って人数を減らさず、何が欠けたのかを出す。
func TestMissingDefinitionIsReported(t *testing.T) {
	set, teamSet, sess := setup(t)
	sess.Members = append(sess.Members, "gone")

	r := Load(set, teamSet, sess)
	if _, ok := r.Get("gone"); ok {
		t.Error("定義の無い相手が名簿に載っている")
	}
	if len(r.Members) != 3 {
		t.Errorf("メンバー数 = %d, want 3", len(r.Members))
	}
	if len(r.Errors) != 1 {
		t.Fatalf("失敗の件数 = %d, want 1", len(r.Errors))
	}
	if !strings.Contains(r.Errors[0].Reason, "gone") {
		t.Errorf("何が欠けたか分からない: %q", r.Errors[0].Reason)
	}
}

// チームエージェントは Tier 0 を持てる。共通で 0 を絞っているのは会話の入口を
// 1 つに保つためで、チームには規定エージェントという入口が無い。
func TestTeamAgentsKeepTierZero(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.AgentPaths = []string{filepath.Join(root, "agents")}
	writeAgent(t, cfg.AgentPaths[0], "peer", def(0))
	writeAgent(t, cfg.TeamAgentDir(), "boss", def(0))

	set := agent.NewSet()
	set.Load(cfg.AgentPaths)
	teamSet := agent.NewSet()
	teamSet.LoadTeam(cfg.TeamAgentPaths())

	if a, _ := set.Get("peer"); a.Tier != agent.MinUserTier {
		t.Errorf("共通の Tier = %d, want %d (引き上げる)", a.Tier, agent.MinUserTier)
	}
	if a, _ := teamSet.Get("boss"); a.Tier != 0 {
		t.Errorf("チームの Tier = %d, want 0 (そのまま)", a.Tier)
	}
	// 共通の一覧にチームエージェントが混ざらないこと。ReadDir がサブ
	// ディレクトリを読まない性質の上に、この分離が乗っている。
	if _, ok := set.Get("boss"); ok {
		t.Error("共通の一覧にチームエージェントが混ざっている")
	}
}

// team/general.json を置かれても規定エージェント扱いにしない。消せず Tier も
// 変えられないチームエージェントができてしまう。
func TestTeamAgentIsNeverFixed(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.AgentPaths = []string{filepath.Join(root, "agents")}
	writeAgent(t, cfg.TeamAgentDir(), agent.DefaultID, def(0))

	teamSet := agent.NewSet()
	teamSet.LoadTeam(cfg.TeamAgentPaths())
	a, ok := teamSet.Get(agent.DefaultID)
	if !ok {
		t.Fatal("読めていない")
	}
	if a.Fixed {
		t.Error("チームエージェントが規定エージェント扱いになっている")
	}
}

// 何を送れるかは Tier の上下だけで決まる。
func TestRelationFollowsTier(t *testing.T) {
	set, teamSet, sess := setup(t)
	r := Load(set, teamSet, sess)

	cases := []struct{ from, to, want string }{
		{"boss", "hand", RelOrder},
		{"hand", "boss", RelReport},
		{"hand", "scout", RelRequest},
		{"boss", "居ない", ""},
	}
	for _, c := range cases {
		if got := r.Relation(c.from, c.to); got != c.want {
			t.Errorf("Relation(%s, %s) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestSubordinatesAndSuperiors(t *testing.T) {
	set, teamSet, sess := setup(t)
	r := Load(set, teamSet, sess)

	ids := func(list []*Member) string {
		var out []string
		for _, m := range list {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(r.Subordinates("boss")); got != "hand,scout" {
		t.Errorf("boss の下位 = %v", got)
	}
	if got := ids(r.Superiors("hand")); got != "boss" {
		t.Errorf("hand の上位 = %v", got)
	}
	if n := len(r.Superiors("boss")); n != 0 {
		t.Errorf("boss に上位が居る: %d", n)
	}
	if n := len(r.Subordinates("hand")); n != 0 {
		t.Errorf("同位を下位として数えている: %d", n)
	}
}

// 窓口はもう無い。名簿にも進め方にも出てはいけない。宛先の行き先だけは
// 書いておく — 書かないと、モデルが「まず窓口へ回します」と補い始める。
func TestNoLeadAnywhere(t *testing.T) {
	set, teamSet, sess := setup(t)
	r := Load(set, teamSet, sess)

	for _, text := range []string{r.Roll("hand"), r.Roll("boss"),
		Guide(r, "hand"), Guide(r, "boss")} {
		if strings.Contains(text, "窓口") {
			t.Errorf("窓口が残っている:\n%s", text)
		}
		if strings.Contains(text, `"*"`) {
			t.Errorf("宛先を委ねる印が残っている:\n%s", text)
		}
	}
	if !strings.Contains(r.Roll("hand"), "名指し") {
		t.Errorf("宛先の決まり方が伝わっていない:\n%s", r.Roll("hand"))
	}
}

// 下位を持つ相手には上司としての進め方を渡す。全員へ同じ文を渡すと、指示を
// 出す側まで「勝手に始めない」「確認を取る」と読み、部下に許可を求め始める。
func TestGuideDependsOnPosition(t *testing.T) {
	set, teamSet, sess := setup(t)
	r := Load(set, teamSet, sess)

	boss := Guide(r, "boss")
	for _, want := range []string{"2 人の下位", "上司", "許可や確認を求めない"} {
		if !strings.Contains(boss, want) {
			t.Errorf("上司向けの進め方に %q が無い:\n%s", want, boss)
		}
	}
	// 上に誰も居ないので、全体を決めるのは自分だと伝える。窓口という指名では
	// なく、Tier の位置から出る性質である。
	if !strings.Contains(boss, "上位のメンバーは居ません") {
		t.Errorf("最上位であることが伝わっていない:\n%s", boss)
	}

	hand := Guide(r, "hand")
	if !strings.Contains(hand, "下位は居ません") {
		t.Errorf("部下向けの進め方になっていない:\n%s", hand)
	}
	if strings.Contains(hand, "上位のメンバーは居ません") {
		t.Error("上位が居るのに最上位として扱っている")
	}
}
