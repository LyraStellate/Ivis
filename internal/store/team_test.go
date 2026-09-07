package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
)

func openTeamStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func put(t *testing.T, st *Store, m *Message) *Message {
	t.Helper()
	if err := st.AppendMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTeamSessionKeepsKindAndMembers(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()

	sess, err := st.CreateTeamSession(ctx, "boss", "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Kind != config.KindTeam {
		t.Errorf("Kind = %q, want %q", sess.Kind, config.KindTeam)
	}
	// 空のチームから始めない。最初の発言の行き先が無い。
	if len(sess.Members) != 1 || sess.Members[0] != "boss" {
		t.Errorf("Members = %v, want [boss]", sess.Members)
	}

	if err := st.SetMembers(ctx, sess.ID, []string{"boss", "hand"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Members, ",") != "boss,hand" {
		t.Errorf("Members = %v", got.Members)
	}
}

// 直列の会話は今までどおり。名簿も持たない。
func TestSeriesSessionUnchanged(t *testing.T) {
	st := openTeamStore(t)
	sess, err := st.CreateSession(context.Background(), "general", "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Kind != config.KindSeries {
		t.Errorf("Kind = %q, want %q", sess.Kind, config.KindSeries)
	}
	if len(sess.Members) != 0 {
		t.Errorf("直列の会話が名簿を持っている: %v", sess.Members)
	}
}

// 各メンバーが読むのは、自分宛てのやり取りと自分自身のツール往復だけ。
// 隣どうしのやり取りは渡らない。人数に比例して入力が太るのを避けるためで、
// 全体像はチケットが持つ。
func TestTeamMessagesShowOnlyOwnTraffic(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, err := st.CreateTeamSession(ctx, "boss", "")
	if err != nil {
		t.Fatal(err)
	}
	s := sess.ID

	put(t, st, &Message{SessionID: s, Role: provider.RoleUser, Content: "依頼", ToAgentID: "boss"})
	put(t, st, &Message{SessionID: s, Role: provider.RoleAssistant, Content: "boss の思考", AgentID: "boss"})
	put(t, st, &Message{SessionID: s, Role: RoleTeam, Content: "boss→hand", AgentID: "boss", ToAgentID: "hand"})
	put(t, st, &Message{SessionID: s, Role: provider.RoleTool, Content: "hand の道具", ToolName: "list_dir", AgentID: "hand"})
	put(t, st, &Message{SessionID: s, Role: RoleTeam, Content: "hand→scout", AgentID: "hand", ToAgentID: "scout"})
	put(t, st, &Message{SessionID: s, Role: RoleTeam, Content: "scout→hand", AgentID: "scout", ToAgentID: "hand"})

	got, err := st.TeamMessages(ctx, s, "hand")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range got {
		texts = append(texts, m.Content)
	}
	// hand が読むのは、自分に届いた 2 通と自分の道具だけ。自分が送った
	// hand→scout は、自分のツール呼び出しとして既に見えているので入らない。
	want := "boss→hand,hand の道具,scout→hand"
	if strings.Join(texts, ",") != want {
		t.Errorf("hand の入力 = %v\nwant %s", texts, want)
	}

	// 利用者の依頼は宛先の boss だけが読む。
	got, err = st.TeamMessages(ctx, s, "boss")
	if err != nil {
		t.Fatal(err)
	}
	texts = nil
	for _, m := range got {
		texts = append(texts, m.Content)
	}
	if strings.Join(texts, ",") != "依頼,boss の思考" {
		t.Errorf("boss の入力 = %v", texts)
	}
}

// 要約は会話に 1 つで、全メンバーが同じものを受け取る。それより前は渡らない。
func TestTeamMessagesStartAtSummary(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")
	s := sess.ID

	put(t, st, &Message{SessionID: s, Role: RoleTeam, Content: "古い", AgentID: "x", ToAgentID: "boss"})
	put(t, st, &Message{SessionID: s, Role: RoleSummary, Content: "まとめ"})
	put(t, st, &Message{SessionID: s, Role: RoleTeam, Content: "新しい", AgentID: "x", ToAgentID: "boss"})

	got, err := st.TeamMessages(ctx, s, "boss")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "まとめ" || got[1].Content != "新しい" {
		t.Errorf("要約より前が渡っている: %d 件", len(got))
	}
}

// 宛先と内訳は保存され、読み出せる。
func TestTeamMessageFieldsRoundTrip(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")

	put(t, st, &Message{SessionID: sess.ID, Role: RoleTeam, AgentID: "hand", ToAgentID: "boss",
		Content: "本文", Why: "頼まれた", Did: "やった", Decision: "受諾"})

	list, err := st.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := list[0]
	if m.ToAgentID != "boss" || m.Why != "頼まれた" || m.Did != "やった" || m.Decision != "受諾" {
		t.Errorf("読み出せていない: %+v", m)
	}
}
