package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// add は 1 件足して ID を返す。
func add(t *testing.T, st *Store, sessionID, parentID, role, content, agentID string) string {
	t.Helper()
	m := &Message{SessionID: sessionID, ParentID: parentID, Role: role, Content: content, AgentID: agentID}
	if err := st.AppendMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// 巻き戻しは起点以降だけを消し、それより前は残す。委譲された子も、親より
// 後の連番を持つのでまとめて消える。
func TestRewind(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	sess, err := st.CreateSession(ctx, "main", "")
	if err != nil {
		t.Fatal(err)
	}

	keepUser := add(t, st, sess.ID, "", provider.RoleUser, "1 回目", "main")
	keepMarker := add(t, st, sess.ID, "", RoleDelegate, "調べて", "child")
	add(t, st, sess.ID, keepMarker, provider.RoleAssistant, "調べました", "child")
	add(t, st, sess.ID, "", provider.RoleAssistant, "できました", "main")

	from := add(t, st, sess.ID, "", provider.RoleUser, "2 回目", "main")
	marker := add(t, st, sess.ID, "", RoleDelegate, "もう一度", "child")
	add(t, st, sess.ID, marker, provider.RoleAssistant, "調べました 2", "child")
	add(t, st, sess.ID, "", provider.RoleAssistant, "できました 2", "main")

	n, text, err := st.Rewind(ctx, sess.ID, from)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("消した件数 = %d, want 4", n)
	}
	if text != "2 回目" {
		t.Errorf("戻す本文 = %q, want %q", text, "2 回目")
	}

	left, err := st.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 4 {
		t.Fatalf("残った件数 = %d, want 4", len(left))
	}
	if left[0].ID != keepUser {
		t.Error("起点より前が消えています")
	}
	// 起点より前の委譲は、子ごと残る。
	var nested int
	for _, m := range left {
		if m.ParentID != "" {
			nested++
		}
	}
	if nested != 1 {
		t.Errorf("残った子の件数 = %d, want 1", nested)
	}
}

// 起点にできるのは最上位の利用者の発言だけ。モデルの発言を起点にできても、
// 消したあとに何を送ればよいかが決まらない。
func TestRewindRejectsNonUser(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	sess, _ := st.CreateSession(ctx, "main", "")

	assistant := add(t, st, sess.ID, "", provider.RoleAssistant, "答え", "main")
	marker := add(t, st, sess.ID, "", RoleDelegate, "依頼", "child")
	child := add(t, st, sess.ID, marker, provider.RoleUser, "子の中の依頼", "child")

	for _, id := range []string{assistant, marker, child} {
		if _, _, err := st.Rewind(ctx, sess.ID, id); !errors.Is(err, ErrNotRewindable) {
			t.Errorf("%s: err = %v, want ErrNotRewindable", id, err)
		}
	}
}

// 別のセッションの発言は起点にできない。
func TestRewindRejectsOtherSession(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	a, _ := st.CreateSession(ctx, "main", "")
	b, _ := st.CreateSession(ctx, "main", "")
	id := add(t, st, a.ID, "", provider.RoleUser, "頼む", "main")

	if _, _, err := st.Rewind(ctx, b.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// 巻き戻すとコンテキスト使用量は不明に戻る。前のターンの値を残すと、消したはずの
// 分を数えたままの割合が出る。
func TestRewindClearsUsage(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	sess, _ := st.CreateSession(ctx, "main", "")
	id := add(t, st, sess.ID, "", provider.RoleUser, "頼む", "main")

	if err := st.SetContextUsage(ctx, sess.ID, 1200, 8192); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetSession(ctx, sess.ID)
	if got.ContextTokens != 1200 || got.ContextLimit != 8192 {
		t.Fatalf("使用量が保存されていません: %+v", got)
	}

	if _, _, err := st.Rewind(ctx, sess.ID, id); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetSession(ctx, sess.ID)
	if got.ContextTokens != 0 || got.ContextLimit != 0 {
		t.Errorf("使用量が残っています: %+v", got)
	}
}

func TestChannelSessionIsUniquePerChannel(t *testing.T) {
	st := open(t)
	ctx := context.Background()

	sess, err := st.CreateChannelSession(ctx, "general", "#dev", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Source != SourceDiscord || sess.ChannelID != "c1" {
		t.Fatalf("出自が残っていない: %+v", sess)
	}

	got, err := st.SessionByChannel(ctx, "c1")
	if err != nil || got.ID != sess.ID {
		t.Fatalf("チャンネルから引けない: %v %+v", err, got)
	}

	// 同じチャンネルに 2 本目を作らせない。作れると、同じ場所の会話が
	// 分かれて、どちらに続きが積まれるか決まらなくなる。
	if _, err := st.CreateChannelSession(ctx, "general", "#dev", "c1"); err == nil {
		t.Fatal("同じチャンネルで 2 本目ができた")
	}
}

func TestWebSessionsHaveNoChannel(t *testing.T) {
	st := open(t)
	ctx := context.Background()

	// 空のチャンネル ID はいくつでも並ぶ。索引が空を除いていないと、
	// 2 本目の Web 会話が作れなくなる。
	for i := 0; i < 3; i++ {
		s, err := st.CreateSession(ctx, "general", "会話")
		if err != nil {
			t.Fatalf("%d 本目で失敗した: %v", i, err)
		}
		if s.Source != SourceWeb || s.ChannelID != "" {
			t.Fatalf("Web の会話に出自が付いている: %+v", s)
		}
	}
}

func TestSessionByChannelMissing(t *testing.T) {
	st := open(t)
	if _, err := st.SessionByChannel(context.Background(), "なし"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("見つからないときに %v", err)
	}
}

// oldSchema は source と channel_id を持たなかった頃のセッション表。
const oldSchema = `
CREATE TABLE sessions (
  id             TEXT PRIMARY KEY,
  title          TEXT NOT NULL,
  agent_id       TEXT NOT NULL,
  context_tokens INTEGER NOT NULL DEFAULT 0,
  context_limit  INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);`

func TestOpenUpgradesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// 既にあるデータベースには CREATE TABLE の変更が届かない。手元の履歴を
	// 抱えたまま更新されるので、ここが通らないと会話が読めなくなる。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, title, agent_id, created_at, updated_at) VALUES ('s1','昔の会話','general',1,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	defer st.Close()

	sess, err := st.GetSession(context.Background(), "s1")
	if err != nil {
		t.Fatalf("前からある会話が読めない: %v", err)
	}
	if sess.Title != "昔の会話" {
		t.Fatalf("中身が変わっている: %+v", sess)
	}
	// 既存の会話は Web のものとして扱う。
	if sess.Source != SourceWeb {
		t.Fatalf("出自が %q", sess.Source)
	}
	if _, err := st.CreateChannelSession(context.Background(), "general", "#dev", "c1"); err != nil {
		t.Fatalf("更新後に Discord の会話を作れない: %v", err)
	}
}

// 圧縮したあと、モデルへ渡すのは最後の要約とそれ以降だけ。元の発言は消さない
// ので、画面と巻き戻しからは従来どおり全件見える (#486237)。
func TestConversationStartsAtTheLastSummary(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	sess, err := st.CreateSession(ctx, "main", "")
	if err != nil {
		t.Fatal(err)
	}

	add(t, st, sess.ID, "", provider.RoleUser, "ひとつめ", "main")
	add(t, st, sess.ID, "", provider.RoleAssistant, "はい", "main")

	// 要約が無いうちは全件。
	input, err := st.ConversationMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 2 {
		t.Fatalf("入力が %d 件。要約が無ければ全件のはず", len(input))
	}

	add(t, st, sess.ID, "", RoleSummary, "これまでの経過", "main")
	add(t, st, sess.ID, "", provider.RoleUser, "ふたつめ", "main")

	input, err = st.ConversationMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 2 || input[0].Role != RoleSummary || input[1].Content != "ふたつめ" {
		t.Fatalf("入力が %d 件で先頭が %q", len(input), input[0].Role)
	}

	// 2 つめの要約ができたら、そこから後ろだけになる。
	add(t, st, sess.ID, "", RoleSummary, "まとめ直し", "main")
	input, err = st.ConversationMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 1 || input[0].Content != "まとめ直し" {
		t.Fatalf("入力が %d 件。最後の要約 1 件のはず", len(input))
	}

	// 画面には全部残っている。
	all, err := st.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("履歴が %d 件。消してはならない", len(all))
	}
}
