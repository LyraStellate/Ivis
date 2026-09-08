package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// v1Schema は前の版が作ったデータベースの形。ここに書いてあるのは「いま動いている手元の
// データベースはこうなっている可能性がある」という事実であって、増やす必要が
// あるのは、列を足すたびに migrations を書き忘れていないかを確かめるため。
//
// CREATE TABLE IF NOT EXISTS は既にある表に列を足さない。スキーマだけ直して
// migrations を忘れると、新しく作ったデータベースでは動き、手元にあるものでは
// 「table ... has no column named ...」で落ちる。試験でその差を作る。
const v1Schema = `
CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  agent_id   TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE messages (
  id         TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  parent_id  TEXT NOT NULL DEFAULT '',
  seq        INTEGER NOT NULL,
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  tool_calls TEXT NOT NULL DEFAULT '',
  tool_name  TEXT NOT NULL DEFAULT '',
  agent_id   TEXT NOT NULL DEFAULT '',
  model      TEXT NOT NULL DEFAULT '',
  error      TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

CREATE TABLE tickets (
  session_id TEXT NOT NULL,
  number     INTEGER NOT NULL,
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  assignee   TEXT NOT NULL DEFAULT '',
  due        TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL,
  priority   TEXT NOT NULL,
  author     TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, number)
);

CREATE TABLE ticket_notes (
  id         TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  number     INTEGER NOT NULL,
  author     TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL,
  auto       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
`

// openOld は前の版の形で作ったデータベースを、いまの Open で開き直す。
func openOld(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(v1Schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("前の版のデータベースを開けない: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// 読み書きに使う列が、前の版のデータベースにも全部そろっていること。
//
// 列を足したのに migrations へ書き忘れると、ここで落ちる。新しく作った
// データベースは通ってしまうので、そちらの試験では気づけない。
func TestMigrationsCoverEveryColumn(t *testing.T) {
	st := openOld(t)
	for _, c := range []struct{ table, columns string }{
		{"sessions", sessionColumns},
		{"messages", messageColumns},
		{"tickets", ticketColumns},
	} {
		// 接続は 1 本しか開かないので、読み終えた行は必ず閉じる。開いたまま
		// 次を引くと、そこで止まったまま返らない。
		rows, err := st.db.Query(`SELECT ` + c.columns + ` FROM ` + c.table + ` LIMIT 0`)
		if err != nil {
			t.Errorf("%s に足りない列があります: %v", c.table, err)
			continue
		}
		rows.Close()
	}
}

// 実際に書けること。列の有無だけでなく、挿入まで通して確かめる。
func TestOldDatabaseStillTakesWrites(t *testing.T) {
	st := openOld(t)
	ctx := context.Background()

	sess, err := st.CreateTeamSession(ctx, "boss", "")
	if err != nil {
		t.Fatalf("会話を作れない: %v", err)
	}
	if err := st.AppendMessage(ctx, &Message{SessionID: sess.ID, Role: RoleTeam,
		AgentID: "boss", ToAgentID: "hand", Content: "頼む", Why: "なぜ", Did: "やった",
		Decision: "受諾"}); err != nil {
		t.Fatalf("発言を書けない: %v", err)
	}

	tk := &Ticket{SessionID: sess.ID, Title: "仕事"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatalf("起票できない: %v", err)
	}
	if _, err := st.ListTickets(ctx, sess.ID, TicketFilter{}); err != nil {
		t.Fatalf("一覧を読めない: %v", err)
	}
	if _, err := st.GetTicket(ctx, sess.ID, tk.Number); err != nil {
		t.Fatalf("1 件を読めない: %v", err)
	}
	// 巻き戻しは seq を見る。列が無ければここで落ちる。
	if _, err := st.deleteTickets(ctx, sess.ID, 0); err != nil {
		t.Fatalf("チケットを消せない: %v", err)
	}
}
