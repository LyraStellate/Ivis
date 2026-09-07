package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// チケットの状態。遷移は縛らない。順序を強制すると、モデルは通れない遷移に
// ぶつかって回り道を始める (別の票を立てる、飛ばすために一度別の状態を経由
// する)。おかしな状態は人が直せばよい (#189542)。
const (
	StatusNew        = "新規"
	StatusInProgress = "進行中"
	StatusResolved   = "解決"
	StatusReview     = "レビュー"
	StatusClosed     = "終了"
)

// Statuses は選べる状態。並びがそのまま画面と指示文に出る。
var Statuses = []string{StatusNew, StatusInProgress, StatusResolved, StatusReview, StatusClosed}

// チケットの優先度。
const (
	PriorityLow    = "低"
	PriorityNormal = "中"
	PriorityHigh   = "高"
	PriorityUrgent = "緊急"
)

// Priorities は選べる優先度。低い順に並べる。並べ替えの重みをこの位置から
// 決めるので、順序そのものが意味を持つ。
var Priorities = []string{PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent}

// PriorityRank は優先度の重み。大きいほど先に出す。知らない値は中と同じに
// 扱う — 一覧から消えるより、真ん中に並ぶほうが気付ける。
func PriorityRank(p string) int {
	for i, v := range Priorities {
		if v == p {
			return i
		}
	}
	return 1
}

// ValidStatus と ValidPriority は受け付けられる値かを返す。
func ValidStatus(v string) bool   { return contains(Statuses, v) }
func ValidPriority(v string) bool { return contains(Priorities, v) }

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Ticket は 1 件の仕事。会話に属し、会話の中で連番を持つ。
//
// 既存の識別子 (24 桁) を使わないのは、会話の中で人にもモデルにも読み上げ
// られないためである。メンバーは「#3 は終わりました」と書く。指せない名前は
// 使われない (#189542)。
type Ticket struct {
	SessionID string `json:"session_id"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// Assignee は担当。空なら未割り当て。1 件につき 1 人しか持たない。
	// 複数人で持つ仕事は分けて起票する。みんなの仕事は誰の仕事でもない。
	Assignee string `json:"assignee"`
	// Due は期限。YYYY-MM-DD の日付で、時刻は持たない。ローカルで動く道具に
	// 分単位の締切は要らない。
	Due      string `json:"due"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	// Author は起票した者。メンバーの ID、または利用者なら空。
	Author    string        `json:"author"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Notes     []*TicketNote `json:"notes,omitempty"`
}

// TicketNote は 1 件の注記。追記のみで、消す手段を設けない。
//
// 概要を書き換えて経過を残さない形にすると、なぜその結論になったかが上書きで
// 消える。判断・議事・参照したリンク・失敗した試みは全部ここへ積む。
type TicketNote struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Author string `json:"author"`
	Body   string `json:"body"`
	// Auto は Ivis が状態や担当の変更から自動で残した行。人が書いたものと
	// 見分けが付かないと、注記が誰の言葉なのか分からなくなる。
	Auto      bool      `json:"auto"`
	CreatedAt time.Time `json:"created_at"`
}

const ticketSchema = `
CREATE TABLE IF NOT EXISTS tickets (
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

CREATE TABLE IF NOT EXISTS ticket_notes (
  id         TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  number     INTEGER NOT NULL,
  author     TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL,
  auto       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ticket_notes ON ticket_notes(session_id, number, created_at);
`

const ticketColumns = `session_id, number, title, body, assignee, due, status, priority,
                       author, created_at, updated_at`

// CreateTicket は起票する。連番はここで振る。
//
// 採番と挿入を 1 つの書き込みにまとめるのは、画面から人が起票するのと手番が
// 起票するのが重なりうるためである。手番自体は 1 つずつ回る (#640275)。
func (s *Store) CreateTicket(ctx context.Context, t *Ticket) error {
	if strings.TrimSpace(t.Title) == "" {
		return errors.New("題を書いてください")
	}
	if t.Status == "" {
		t.Status = StatusNew
	}
	if t.Priority == "" {
		t.Priority = PriorityNormal
	}
	if !ValidStatus(t.Status) {
		return fmt.Errorf("状態は %s のいずれかです", strings.Join(Statuses, " / "))
	}
	if !ValidPriority(t.Priority) {
		return fmt.Errorf("優先度は %s のいずれかです", strings.Join(Priorities, " / "))
	}
	now := time.Now()
	t.CreatedAt, t.UpdatedAt = now, now

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var max sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(number) FROM tickets WHERE session_id = ?`, t.SessionID).Scan(&max); err != nil {
		return err
	}
	t.Number = int(max.Int64) + 1

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tickets (`+ticketColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.SessionID, t.Number, t.Title, t.Body, t.Assignee, t.Due, t.Status, t.Priority,
		t.Author, now.UnixMilli(), now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTicket は 1 件を注記ごと返す。
func (s *Store) GetTicket(ctx context.Context, sessionID string, number int) (*Ticket, error) {
	var t Ticket
	var created, updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT `+ticketColumns+` FROM tickets WHERE session_id = ? AND number = ?`,
		sessionID, number).
		Scan(&t.SessionID, &t.Number, &t.Title, &t.Body, &t.Assignee, &t.Due, &t.Status,
			&t.Priority, &t.Author, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.UnixMilli(created)
	t.UpdatedAt = time.UnixMilli(updated)

	notes, err := s.TicketNotes(ctx, sessionID, number)
	if err != nil {
		return nil, err
	}
	t.Notes = notes
	return &t, nil
}

// TicketFilter は一覧の絞り込み。
type TicketFilter struct {
	Assignee string
	Status   string
	// IncludeClosed が偽なら終了を除く。終わった仕事が既定で並ぶと、
	// 残っているものが埋もれる。
	IncludeClosed bool
}

// ListTickets は絞り込んだ一覧を返す。注記は含めない。
//
// 一覧で本文と注記まで返すと、それだけでコンテキストが埋まる。中身が要るなら
// 番号を指定して 1 件を読む。
func (s *Store) ListTickets(ctx context.Context, sessionID string, f TicketFilter) ([]*Ticket, error) {
	q := `SELECT ` + ticketColumns + ` FROM tickets WHERE session_id = ?`
	args := []any{sessionID}
	if f.Assignee != "" {
		q += ` AND assignee = ?`
		args = append(args, f.Assignee)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	} else if !f.IncludeClosed {
		q += ` AND status <> ?`
		args = append(args, StatusClosed)
	}
	q += ` ORDER BY number ASC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Ticket
	for rows.Next() {
		var t Ticket
		var created, updated int64
		if err := rows.Scan(&t.SessionID, &t.Number, &t.Title, &t.Body, &t.Assignee, &t.Due,
			&t.Status, &t.Priority, &t.Author, &created, &updated); err != nil {
			return nil, err
		}
		t.CreatedAt = time.UnixMilli(created)
		t.UpdatedAt = time.UnixMilli(updated)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// TicketPatch は変えたい項目だけを持つ。nil の項目は変えない。
//
// 値ではなく指し手で受けるのは、「空文字にする」と「変えない」を区別する
// ためである。担当を外す操作は、空文字を入れることでしか表せない。
type TicketPatch struct {
	Title    *string
	Body     *string
	Assignee *string
	Due      *string
	Status   *string
	Priority *string
}

// UpdateTicket は項目を書き換え、状態と担当が動いたときは注記を 1 行残す。
//
// 履歴の表を別に持たないのは、読む場所が 2 つに分かれると片方しか読まれない
// からである。経過は注記の列に一本化する。
func (s *Store) UpdateTicket(ctx context.Context, sessionID string, number int,
	patch TicketPatch, author string) (*Ticket, error) {
	cur, err := s.GetTicket(ctx, sessionID, number)
	if err != nil {
		return nil, err
	}
	if patch.Status != nil && !ValidStatus(*patch.Status) {
		return nil, fmt.Errorf("状態は %s のいずれかです", strings.Join(Statuses, " / "))
	}
	if patch.Priority != nil && !ValidPriority(*patch.Priority) {
		return nil, fmt.Errorf("優先度は %s のいずれかです", strings.Join(Priorities, " / "))
	}
	if patch.Title != nil && strings.TrimSpace(*patch.Title) == "" {
		return nil, errors.New("題を空にはできません")
	}

	next := *cur
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&next.Title, patch.Title)
	set(&next.Body, patch.Body)
	set(&next.Assignee, patch.Assignee)
	set(&next.Due, patch.Due)
	set(&next.Status, patch.Status)
	set(&next.Priority, patch.Priority)

	now := time.Now()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tickets SET title = ?, body = ?, assignee = ?, due = ?, status = ?,
		                    priority = ?, updated_at = ?
		  WHERE session_id = ? AND number = ?`,
		next.Title, next.Body, next.Assignee, next.Due, next.Status, next.Priority,
		now.UnixMilli(), sessionID, number); err != nil {
		return nil, err
	}

	if next.Status != cur.Status {
		_ = s.AddNote(ctx, sessionID, number, author,
			fmt.Sprintf("状態: %s → %s", cur.Status, next.Status), true)
	}
	if next.Assignee != cur.Assignee {
		_ = s.AddNote(ctx, sessionID, number, author,
			fmt.Sprintf("担当: %s → %s", orNobody(cur.Assignee), orNobody(next.Assignee)), true)
	}
	return s.GetTicket(ctx, sessionID, number)
}

func orNobody(id string) string {
	if id == "" {
		return "(未割り当て)"
	}
	return id
}

// AddNote は注記を 1 件足す。
func (s *Store) AddNote(ctx context.Context, sessionID string, number int,
	author, body string, auto bool) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("注記が空です")
	}
	flag := 0
	if auto {
		flag = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_notes (id, session_id, number, author, body, auto, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		NewID(), sessionID, number, author, body, flag, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE tickets SET updated_at = ? WHERE session_id = ? AND number = ?`,
		time.Now().UnixMilli(), sessionID, number)
	return err
}

// TicketNotes は 1 件の注記を古い順に返す。
func (s *Store) TicketNotes(ctx context.Context, sessionID string, number int) ([]*TicketNote, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, number, author, body, auto, created_at FROM ticket_notes
		  WHERE session_id = ? AND number = ? ORDER BY created_at ASC, rowid ASC`,
		sessionID, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*TicketNote
	for rows.Next() {
		var n TicketNote
		var auto int
		var created int64
		if err := rows.Scan(&n.ID, &n.Number, &n.Author, &n.Body, &auto, &created); err != nil {
			return nil, err
		}
		n.Auto = auto != 0
		n.CreatedAt = time.UnixMilli(created)
		out = append(out, &n)
	}
	return out, rows.Err()
}

// DeleteTicket は 1 件を注記ごと消す。
//
// 消せるのは人だけである。モデルに消させないのは、消えたことが誰にも見えない
// からで、注記はその消えた票に付いていた (#189542)。
func (s *Store) DeleteTicket(ctx context.Context, sessionID string, number int) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tickets WHERE session_id = ? AND number = ?`, sessionID, number)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM ticket_notes WHERE session_id = ? AND number = ?`, sessionID, number)
	return err
}

// deleteTickets は会話に属するチケットを消す。会話を消すときに呼ぶ。
func (s *Store) deleteTickets(ctx context.Context, sessionID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM ticket_notes WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tickets WHERE session_id = ?`, sessionID)
	return err
}
