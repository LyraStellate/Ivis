package team

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/store"
)

// migrateFixture は、会話ごとに定義を置いていた頃の状態を作る。
func migrateFixture(t *testing.T) (*config.Config, *store.Store) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.AgentPaths = []string{filepath.Join(root, "agents")}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return cfg, st
}

func teamAgentIDs(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	found, _ := agent.ReadDir(cfg.TeamAgentDir())
	var out []string
	for _, a := range found {
		out = append(out, a.ID)
	}
	return out
}

// 会話ごとの定義が、共有の置き場へ移り、その会話で有効なまま残ること。
func TestMigrateMovesAndEnables(t *testing.T) {
	cfg, st := migrateFixture(t)
	ctx := context.Background()

	sess, err := st.CreateTeamSession(ctx, "general", "")
	if err != nil {
		t.Fatal(err)
	}
	writeAgent(t, cfg.LegacySessionAgentsDir(sess.ID), "reviewer", def(2))

	res, err := MigrateSessionAgents(ctx, cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 1 || res.Sessions != 1 {
		t.Fatalf("結果 = %+v", res)
	}
	if got := teamAgentIDs(t, cfg); strings.Join(got, ",") != "reviewer" {
		t.Errorf("移った先 = %v", got)
	}
	// 元の置き場は残らない。無いこと自体が「移行済み」の記録になる。
	if _, err := os.Stat(cfg.LegacySessionAgentsDir(sess.ID)); !os.IsNotExist(err) {
		t.Errorf("元のディレクトリが残っている: %v", err)
	}

	got, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got.Members, "reviewer") {
		t.Errorf("移した先が有効になっていない: %v", got.Members)
	}
}

// 2 つの会話が同じ名前を持っているのは普通に起きる。後から来たほうに番号を
// 付ける。中断せずに最後まで移す。
func TestMigrateRenamesOnCollision(t *testing.T) {
	cfg, st := migrateFixture(t)
	ctx := context.Background()

	a, _ := st.CreateTeamSession(ctx, "general", "A")
	b, _ := st.CreateTeamSession(ctx, "general", "B")
	writeAgent(t, cfg.LegacySessionAgentsDir(a.ID), "reviewer", def(2))
	writeAgent(t, cfg.LegacySessionAgentsDir(b.ID), "reviewer", def(3))

	res, err := MigrateSessionAgents(ctx, cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 2 || res.Sessions != 2 {
		t.Fatalf("結果 = %+v", res)
	}
	// どちらの会話が素の名前を取るかは、会話 ID の並びで決まる。決めるのは
	// ここではないので、名前そのものではなく「2 つが別名で揃っていること」を
	// 見る。
	got := teamAgentIDs(t, cfg)
	sort.Strings(got)
	if strings.Join(got, ",") != "reviewer,reviewer-2" {
		t.Errorf("移った先 = %v", got)
	}
	if r := res.Renamed(); len(r) != 1 || r[0].To != "reviewer-2" {
		t.Errorf("改名の記録 = %+v", r)
	}

	// それぞれの会話で、自分の分だけが有効になっていること。混ざると、
	// もう一方の会話の担当が名簿に現れる。
	ga, _ := st.GetSession(ctx, a.ID)
	gb, _ := st.GetSession(ctx, b.ID)
	na := enabledOf(ga.Members)
	nb := enabledOf(gb.Members)
	if len(na) != 1 || len(nb) != 1 || na[0] == nb[0] {
		t.Fatalf("名簿が混ざっている: A=%v B=%v", ga.Members, gb.Members)
	}
	both := []string{na[0], nb[0]}
	sort.Strings(both)
	if strings.Join(both, ",") != "reviewer,reviewer-2" {
		t.Errorf("有効になった名前 = %v", both)
	}
}

// enabledOf は、移行で足された分だけを返す。会話を作るときに入る種
// (general) は数えない。
func enabledOf(members []string) []string {
	var out []string
	for _, m := range members {
		if strings.HasPrefix(m, "reviewer") {
			out = append(out, m)
		}
	}
	return out
}

// 共通エージェントと同じ名前も避ける。同じ名前があると、その共通を有効に
// した会話で宛先が決まらない。
func TestMigrateAvoidsCommonNames(t *testing.T) {
	cfg, st := migrateFixture(t)
	ctx := context.Background()
	writeAgent(t, cfg.AgentPaths[0], "reviewer", def(1))

	sess, _ := st.CreateTeamSession(ctx, "general", "")
	writeAgent(t, cfg.LegacySessionAgentsDir(sess.ID), "reviewer", def(2))

	if _, err := MigrateSessionAgents(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	if got := teamAgentIDs(t, cfg); strings.Join(got, ",") != "reviewer-2" {
		t.Errorf("移った先 = %v (共通と同じ名前を避けていない)", got)
	}
}

// 名前が変わったら、過去の発言とチケットの担当も書き換える。書き換えないと、
// 過去の吹き出しと担当チケットが別人の名前と色で描かれる。
func TestMigrateRewritesPastRecords(t *testing.T) {
	cfg, st := migrateFixture(t)
	ctx := context.Background()
	writeAgent(t, cfg.AgentPaths[0], "reviewer", def(1))

	sess, _ := st.CreateTeamSession(ctx, "general", "")
	writeAgent(t, cfg.LegacySessionAgentsDir(sess.ID), "reviewer", def(2))

	if err := st.AppendMessage(ctx, &store.Message{SessionID: sess.ID, Role: store.RoleTeam,
		AgentID: "reviewer", ToAgentID: "general", Content: "見ました"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(ctx, &store.Message{SessionID: sess.ID, Role: store.RoleTeam,
		AgentID: "general", ToAgentID: "reviewer", Content: "見て"}); err != nil {
		t.Fatal(err)
	}
	tk := &store.Ticket{SessionID: sess.ID, Title: "確認", Assignee: "reviewer", Author: "reviewer"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if err := st.AddNote(ctx, sess.ID, tk.Number, "reviewer", "調べた", false); err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateSessionAgents(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}

	msgs, _ := st.ListMessages(ctx, sess.ID)
	for _, m := range msgs {
		if m.AgentID == "reviewer" || m.ToAgentID == "reviewer" {
			t.Errorf("古い ID が残っている: %+v", m)
		}
	}
	if msgs[0].AgentID != "reviewer-2" || msgs[1].ToAgentID != "reviewer-2" {
		t.Errorf("書き換わっていない: %q / %q", msgs[0].AgentID, msgs[1].ToAgentID)
	}

	got, _ := st.GetTicket(ctx, sess.ID, tk.Number)
	if got.Assignee != "reviewer-2" || got.Author != "reviewer-2" {
		t.Errorf("チケット = 担当 %q / 起票 %q", got.Assignee, got.Author)
	}
	if got.Notes[0].Author != "reviewer-2" {
		t.Errorf("注記の書き手 = %q", got.Notes[0].Author)
	}
}

// 2 度実行しても何も起きない。元のディレクトリが無いこと自体が記録になる。
func TestMigrateIsIdempotent(t *testing.T) {
	cfg, st := migrateFixture(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "general", "")
	writeAgent(t, cfg.LegacySessionAgentsDir(sess.ID), "reviewer", def(2))

	if _, err := MigrateSessionAgents(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	res, err := MigrateSessionAgents(ctx, cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 0 {
		t.Errorf("2 度目で何かを移している: %+v", res)
	}
	if got := teamAgentIDs(t, cfg); len(got) != 1 {
		t.Errorf("定義が増えている: %v", got)
	}
}

// 壊れた定義も移す。解析して弾くと、そこだけ取り残されて孤児になる。
// 移した先で読み込みの失敗として見えるのが正しい行き先である。
func TestMigrateMovesBrokenDefinitions(t *testing.T) {
	cfg, st := migrateFixture(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "general", "")

	dir := cfg.LegacySessionAgentsDir(sess.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// model が無く、読み込みに失敗する定義。
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateSessionAgents(ctx, cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 1 {
		t.Fatalf("壊れた定義が取り残されている: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.TeamAgentDir(), "broken.json")); err != nil {
		t.Errorf("移っていない: %v", err)
	}
	// 移した先で失敗として見えること。
	_, errs := agent.ReadDir(cfg.TeamAgentDir())
	if len(errs) != 1 {
		t.Errorf("読み込みの失敗として出ていない: %v", errs)
	}
}

// データベースに無い会話のディレクトリが残っていることはある。定義は移し、
// 有効化だけを飛ばす。
func TestMigrateSurvivesOrphanDirectory(t *testing.T) {
	cfg, st := migrateFixture(t)
	writeAgent(t, cfg.LegacySessionAgentsDir("消えた会話"), "ghost", def(2))

	res, err := MigrateSessionAgents(context.Background(), cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 1 {
		t.Errorf("移していない: %+v", res)
	}
	if got := teamAgentIDs(t, cfg); strings.Join(got, ",") != "ghost" {
		t.Errorf("移った先 = %v", got)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
