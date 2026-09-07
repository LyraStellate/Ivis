// Package store はセッションと会話履歴を SQLite に永続化する。
//
// エージェント定義とスキルはファイルが正だが、履歴はここが正である。
// 一覧表示のたびに全ファイルを走査させないための選択。
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"

	_ "modernc.org/sqlite" // 純 Go の SQLite ドライバ。単一バイナリの前提を崩さない。
)

// ErrNotFound は対象が存在しないこと。
var ErrNotFound = errors.New("見つかりません")

// RoleDelegate は委譲の起点として記録する印。会話の役割ではないため
// provider の定数とは別に持つ。
const RoleDelegate = "delegate"

// 会話の出自。どこから始まった会話かで、できる操作が変わる (#617204)。
const (
	SourceWeb     = "web"
	SourceDiscord = "discord"
)

// RoleTeam はチームセッションでメンバーが別のメンバーへ送ったメッセージ。
// 会話の役割ではなく Ivis が置く印で、委譲の起点 ("delegate") と同じ扱いに
// なる。送り手は AgentID、宛先は ToAgentID である (#640275)。
const RoleTeam = "team"

// RoleSummary は圧縮でまとめた発言。提供元の役割ではなく Ivis が置く印で、
// 委譲の起点 ("delegate") と同じ扱いになる。
//
// これより前の発言はモデルへ渡さない。消しはしないので、画面には残り、
// 巻き戻せば圧縮前へ戻る (#486237)。
const RoleSummary = "summary"

// Session は 1 つの会話。エージェント定義が後から削除されても ID は保持し続ける。
type Session struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	AgentID string `json:"agent_id"`
	// Source は会話の出自。既存の会話は web として扱う。
	Source string `json:"source"`
	// Kind は会話の進み方。直列 (series) かチーム (team) か。既存の会話は
	// 直列として扱う。作成時に決まり、後から変わらない (#731906)。
	Kind string `json:"kind"`
	// Members はこの会話に参加している共通エージェントの ID。セッション固有の
	// エージェントは定義が在ること自体が参加なので、ここには載らない。
	// 直列の会話では常に空である。
	Members []string `json:"members,omitempty"`
	// ChannelID は Discord から始まった会話が対応するチャンネル。
	// Web の会話では空。空でない値は会話をまたいで一意である。
	ChannelID string `json:"channel_id,omitempty"`
	// ContextTokens は直前のターンでモデルへ送った入力のトークン数、
	// ContextLimit はそのときのコンテキスト長。会話を開き直したときに、生成を待たず
	// 前回の状態を出せるようにするために持つ。発言ごとに持たないのは、
	// 見せたいのが「いま次を送れるか」であって推移ではないため。
	ContextTokens int       `json:"context_tokens"`
	ContextLimit  int       `json:"context_limit"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Message は会話の 1 発言。
//
// ParentID は委譲の構造を表す。子エージェントの実行過程を親の会話に平坦に
// 混ぜると後から構造を復元できないため、親メッセージにぶら下げて保持する。
type Message struct {
	ID        string              `json:"id"`
	SessionID string              `json:"session_id"`
	ParentID  string              `json:"parent_id,omitempty"`
	Seq       int64               `json:"seq"`
	Role      string              `json:"role"`
	Content   string              `json:"content"`
	ToolCalls []provider.ToolCall `json:"tool_calls,omitempty"`
	ToolName  string              `json:"tool_name,omitempty"`
	// Thinking はモデルが答えに至るまでの過程。本文と分けて持つ。
	Thinking string `json:"thinking,omitempty"`
	// ToAgentID は宛先のメンバー。チームセッションのメッセージだけが持つ。
	// 送り手は AgentID である (#640275)。
	ToAgentID string `json:"to_agent_id,omitempty"`
	// Why は手番を取ることになった理由、Did はその手番で行ったこと。受け手は
	// この会話のやり取りを見られないので、この 2 つは飾りではなく渡る情報の
	// 全部である。本文へ畳み込まないのは、画面が別々に描くためと、後から
	// 切り出す形にしないためである。
	Why string `json:"why,omitempty"`
	Did string `json:"did,omitempty"`
	// Decision は依頼への返答のときだけ、受諾か却下か。
	Decision  string    `json:"decision,omitempty"`
	AgentID   string    `json:"agent_id"`
	Model     string    `json:"model,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store は履歴データベース。
type Store struct {
	db *sql.DB
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sessions (
  id             TEXT PRIMARY KEY,
  title          TEXT NOT NULL,
  agent_id       TEXT NOT NULL,
  source         TEXT NOT NULL DEFAULT 'web',
  channel_id     TEXT NOT NULL DEFAULT '',
  kind           TEXT NOT NULL DEFAULT 'series',
  members        TEXT NOT NULL DEFAULT '',
  context_tokens INTEGER NOT NULL DEFAULT 0,
  context_limit  INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
  id           TEXT PRIMARY KEY,
  session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  parent_id    TEXT NOT NULL DEFAULT '',
  seq          INTEGER NOT NULL,
  role         TEXT NOT NULL,
  content      TEXT NOT NULL,
  tool_calls   TEXT NOT NULL DEFAULT '',
  tool_name    TEXT NOT NULL DEFAULT '',
  agent_id     TEXT NOT NULL DEFAULT '',
  model        TEXT NOT NULL DEFAULT '',
  thinking     TEXT NOT NULL DEFAULT '',
  to_agent_id  TEXT NOT NULL DEFAULT '',
  why          TEXT NOT NULL DEFAULT '',
  did          TEXT NOT NULL DEFAULT '',
  decision     TEXT NOT NULL DEFAULT '',
  error        TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC);
`

// channelIndex はチャンネルと会話を 1 対 1 に保つ。空を除くのは、Web の
// 会話がいくらでも空のチャンネル ID を持つためである。
const channelIndex = `CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_channel
                        ON sessions(channel_id) WHERE channel_id <> ''`

// migrations は既存のデータベースへ後から加える列。順に試し、失敗は無視する。
var migrations = []string{
	`ALTER TABLE messages ADD COLUMN thinking TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN context_tokens INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE sessions ADD COLUMN context_limit INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE sessions ADD COLUMN source TEXT NOT NULL DEFAULT 'web'`,
	`ALTER TABLE sessions ADD COLUMN channel_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'series'`,
	`ALTER TABLE sessions ADD COLUMN members TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN to_agent_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN why TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN did TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN decision TEXT NOT NULL DEFAULT ''`,
	channelIndex,
}

// Open はデータベースを開き、必要ならスキーマを作る。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("履歴データベースを開けませんでした: %w", err)
	}
	// 書き込みの競合を避けるため、接続は 1 本に絞る。単一利用者の前提と釣り合う。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema + ticketSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("履歴データベースを初期化できませんでした: %w", err)
	}
	// 既に作られているデータベースには CREATE TABLE の変更が届かない。
	// 足りない列だけを後から加える。既にあれば失敗するので、それは無視する。
	for _, stmt := range migrations {
		db.Exec(stmt)
	}
	return &Store{db: db}, nil
}

// Close はデータベースを閉じる。
func (s *Store) Close() error { return s.db.Close() }

// NewID は新しい識別子を返す。
func NewID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 乱数が読めない環境は想定しない。時刻で代替する。
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// CreateSession は新しいセッションを作る。
func (s *Store) CreateSession(ctx context.Context, agentID, title string) (*Session, error) {
	return s.createSession(ctx, agentID, title, SourceWeb, "", config.KindSeries, nil)
}

// CreateTeamSession は複数のメンバーが手番を回す会話を作る。
//
// 空のチームから始めない。最初の発言の行き先が無く、画面は「誰も居ません」と
// しか言えないためで、指定されたエージェントを窓口として 1 体だけ参加させた
// 状態で始める (#731906)。
func (s *Store) CreateTeamSession(ctx context.Context, agentID, title string) (*Session, error) {
	return s.createSession(ctx, agentID, title, SourceWeb, "", config.KindTeam, []string{agentID})
}

// CreateChannelSession は Discord のチャンネルに対応する会話を作る。
//
// チャンネルとの対応は索引で一意に保たれるので、同じチャンネルで二重に
// 呼ばれた場合は失敗する。呼ぶ側は引き当ててから作ることになる。
func (s *Store) CreateChannelSession(ctx context.Context, agentID, title, channelID string) (*Session, error) {
	if channelID == "" {
		return nil, errors.New("チャンネルが指定されていません")
	}
	return s.createSession(ctx, agentID, title, SourceDiscord, channelID, config.KindSeries, nil)
}

func (s *Store) createSession(ctx context.Context, agentID, title, source, channelID, kind string,
	members []string) (*Session, error) {
	if title == "" {
		title = "新しい会話"
	}
	// 直列の会話は名簿を持たない。持たせても読む者が居らず、チームだけが
	// 見る値が全ての会話に散る。
	if kind != config.KindTeam {
		members = nil
	}
	now := time.Now()
	sess := &Session{ID: NewID(), Title: title, AgentID: agentID, Source: source,
		ChannelID: channelID, Kind: kind, Members: members, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, title, agent_id, source, channel_id, kind, members,
		                       created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Title, sess.AgentID, sess.Source, sess.ChannelID, sess.Kind,
		encodeMembers(members), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// encodeMembers と decodeMembers は名簿の入出力。空の一覧は空文字にする。
// 空配列と空文字が混ざると、読み出すたびにどちらかへ畳む処理が要る。
func encodeMembers(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeMembers(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// SetMembers は参加している共通エージェントの一覧を置き換える。
func (s *Store) SetMembers(ctx context.Context, sessionID string, ids []string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET members = ?, updated_at = ? WHERE id = ?`,
		encodeMembers(ids), time.Now().UnixMilli(), sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const sessionColumns = `id, title, agent_id, source, channel_id, kind, members,
                        context_tokens, context_limit, created_at, updated_at`

// ListSessions は更新の新しい順に一覧を返す。
func (s *Store) ListSessions(ctx context.Context) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Session
	for rows.Next() {
		var sess Session
		var members string
		var created, updated int64
		if err := rows.Scan(&sess.ID, &sess.Title, &sess.AgentID, &sess.Source, &sess.ChannelID,
			&sess.Kind, &members, &sess.ContextTokens, &sess.ContextLimit, &created, &updated); err != nil {
			return nil, err
		}
		sess.Members = decodeMembers(members)
		sess.CreatedAt = time.UnixMilli(created)
		sess.UpdatedAt = time.UnixMilli(updated)
		out = append(out, &sess)
	}
	return out, rows.Err()
}

// GetSession は 1 件返す。
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	return s.oneSession(ctx, `WHERE id = ?`, id)
}

// SessionByChannel は Discord のチャンネルに対応する会話を返す。
// 対応がなければ ErrNotFound。
func (s *Store) SessionByChannel(ctx context.Context, channelID string) (*Session, error) {
	if channelID == "" {
		return nil, ErrNotFound
	}
	return s.oneSession(ctx, `WHERE channel_id = ?`, channelID)
}

func (s *Store) oneSession(ctx context.Context, where string, arg any) (*Session, error) {
	var sess Session
	var members string
	var created, updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions `+where, arg).
		Scan(&sess.ID, &sess.Title, &sess.AgentID, &sess.Source, &sess.ChannelID,
			&sess.Kind, &members, &sess.ContextTokens, &sess.ContextLimit, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.Members = decodeMembers(members)
	sess.CreatedAt = time.UnixMilli(created)
	sess.UpdatedAt = time.UnixMilli(updated)
	return &sess, nil
}

// UpdateSession はタイトルとエージェントを更新する。空文字の項目は変更しない。
func (s *Store) UpdateSession(ctx context.Context, id, title, agentID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions
		    SET title = CASE WHEN ? <> '' THEN ? ELSE title END,
		        agent_id = CASE WHEN ? <> '' THEN ? ELSE agent_id END,
		        updated_at = ?
		  WHERE id = ?`,
		title, title, agentID, agentID, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSession はセッションとそのメッセージだけを消す。
// エージェント定義とスキルには一切影響しない。
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, id); err != nil {
		return err
	}
	// チケットも会話に属する。仕事の記録はその会話のものであり、読み手の
	// 居なくなった票を残しても、どの会話のものか辿れない (#189542)。
	return s.deleteTickets(ctx, id)
}

// AppendMessage は発言を追加する。ID と連番と時刻はここで割り当てる。
func (s *Store) AppendMessage(ctx context.Context, m *Message) error {
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	var seq sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM messages WHERE session_id = ?`, m.SessionID).Scan(&seq); err != nil {
		return err
	}
	m.Seq = seq.Int64 + 1

	var calls string
	if len(m.ToolCalls) > 0 {
		b, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return err
		}
		calls = string(b)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, session_id, parent_id, seq, role, content, tool_calls,
		                       tool_name, thinking, to_agent_id, why, did, decision,
		                       agent_id, model, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.ParentID, m.Seq, m.Role, m.Content, calls, m.ToolName,
		m.Thinking, m.ToAgentID, m.Why, m.Did, m.Decision,
		m.AgentID, m.Model, m.Error, m.CreatedAt.UnixMilli())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`,
		time.Now().UnixMilli(), m.SessionID)
	return err
}

// UpdateMessage は本文とエラーを書き換える。生成の途中でプロセスが落ちても
// それまでの内容が失われないよう、ストリーミング中も一定間隔でこれを呼ぶ。
func (s *Store) UpdateMessage(ctx context.Context, id, content, thinking, errText string, calls []provider.ToolCall) error {
	var raw string
	if len(calls) > 0 {
		b, err := json.Marshal(calls)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET content = ?, thinking = ?, error = ?, tool_calls = ? WHERE id = ?`,
		content, thinking, errText, raw, id)
	return err
}

const messageColumns = `id, session_id, parent_id, seq, role, content, tool_calls,
                        tool_name, thinking, to_agent_id, why, did, decision,
                        agent_id, model, error, created_at`

// ListMessages はセッションの全発言を連番順に返す。委譲の子も含む。
// UI 側で parent_id により折りたたんで表示する。
func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE session_id = ? ORDER BY seq ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// ConversationMessages は最上位の発言だけを返す。次のターンの入力を組み立てる
// のに使う。子エージェントの途中経過は親のコンテキストを圧迫しないよう含めない。
//
// 要約 (RoleSummary) があれば、最後のものとそれ以降だけを返す。圧縮したあとの
// 入力はそこから始まる。元の発言は消していないので、画面や巻き戻しからは
// 従来どおり全件見える (#486237)。
func (s *Store) ConversationMessages(ctx context.Context, sessionID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages
		  WHERE session_id = ? AND parent_id = ''
		    AND seq >= COALESCE((SELECT MAX(seq) FROM messages
		                          WHERE session_id = ? AND parent_id = '' AND role = ?), 0)
		  ORDER BY seq ASC`, sessionID, sessionID, RoleSummary)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// TeamMessages はそのメンバーがモデルへ渡されるべき発言だけを連番順に返す。
//
// 渡すのは、自分に宛てられたメッセージと、自分自身のツール往復である。ほかの
// 人どうしのやり取りと、ほかの人のツール往復は渡さない。人数に比例して入力が
// 太り、ローカルモデルでは数体で溢れるためで、全体像は会話ではなくチケットが
// 持つ (#258413)。
//
// 自分が送ったメッセージをここに含めないのは、送った本人にはそれが自分の
// ツール呼び出しとして既に見えているからである。両方入れると同じ文が 2 度並ぶ。
//
// 要約 (RoleSummary) の扱いは ConversationMessages と同じで、最後のものと
// それ以降だけを返す。要約は会話に 1 つで、全メンバーが同じものを受け取る。
func (s *Store) TeamMessages(ctx context.Context, sessionID, agentID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages
		  WHERE session_id = ? AND parent_id = ''
		    AND seq >= COALESCE((SELECT MAX(seq) FROM messages
		                          WHERE session_id = ? AND parent_id = '' AND role = ?), 0)
		    AND ( role = ?
		       OR (role IN (?, ?) AND to_agent_id = ?)
		       OR (role IN (?, ?) AND agent_id = ?) )
		  ORDER BY seq ASC`,
		sessionID, sessionID, RoleSummary, RoleSummary,
		RoleTeam, provider.RoleUser, agentID,
		provider.RoleAssistant, provider.RoleTool, agentID)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

func scanMessages(rows *sql.Rows) ([]*Message, error) {
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		var m Message
		var calls string
		var created int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.ParentID, &m.Seq, &m.Role, &m.Content,
			&calls, &m.ToolName, &m.Thinking, &m.ToAgentID, &m.Why, &m.Did, &m.Decision,
			&m.AgentID, &m.Model, &m.Error, &created); err != nil {
			return nil, err
		}
		if calls != "" {
			if err := json.Unmarshal([]byte(calls), &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		m.CreatedAt = time.UnixMilli(created)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// Delegation は過去に受けた依頼と、それに対する最終的な応答の組。
type Delegation struct {
	Task   string
	Result string
}

// PastDelegations は、そのエージェントがこのセッションで過去に受けた依頼と
// 応答を古い順に返す。記憶を有効にしたエージェントの引き継ぎに使う。
//
// 途中のツール実行は含めない。含めると子の入力が回数に比例して膨らみ、
// 引き継ぎたい内容 (何を頼まれ、何を答えたか) が埋もれる。
func (s *Store) PastDelegations(ctx context.Context, sessionID, agentID string) ([]Delegation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.content,
		        COALESCE((SELECT c.content FROM messages c
		                   WHERE c.session_id = m.session_id AND c.parent_id = m.id
		                     AND c.role = 'assistant' AND c.content <> ''
		                   ORDER BY c.seq DESC LIMIT 1), '')
		   FROM messages m
		  WHERE m.session_id = ? AND m.agent_id = ? AND m.role = ?
		  ORDER BY m.seq ASC`, sessionID, agentID, RoleDelegate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Delegation
	for rows.Next() {
		var d Delegation
		if err := rows.Scan(&d.Task, &d.Result); err != nil {
			return nil, err
		}
		if d.Result == "" {
			// 応答が残っていない回は引き継ぐ材料にならない。
			continue
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ErrNotRewindable は巻き戻しの起点にできない発言を指したこと。
var ErrNotRewindable = errors.New("この発言からは巻き戻せません")

// SetContextUsage は直前のターンのコンテキスト使用量を記録する。
func (s *Store) SetContextUsage(ctx context.Context, sessionID string, tokens, limit int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET context_tokens = ?, context_limit = ? WHERE id = ?`,
		tokens, limit, sessionID)
	return err
}

// ClearMessages はその会話の発言をすべて消す。会話そのものは残す。
//
// 会話ごと消さないのは、Discord のチャンネルに結び付いているためである。
// 消して作り直すと結び付きが張り直され、そのときに作業ディレクトリも別の
// 場所になる。話の続きを捨てたいだけの人が、置いたファイルまで失う。
func (s *Store) ClearMessages(ctx context.Context, sessionID string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, sessionID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	// 実測値が無くなったので使用量は不明に戻す。前のターンの値を残すと、
	// 消したはずの分を数えたままの割合が出る。
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET context_tokens = 0, context_limit = 0, updated_at = ? WHERE id = ?`,
		time.Now().UnixMilli(), sessionID); err != nil {
		return 0, err
	}
	return int(n), nil
}

// Rewind は指定した利用者の発言と、それ以降の全ての発言を消す。
// 消した件数と、入力欄へ戻す本文を返す。
//
// 起点を利用者の発言に限るのは、やり直しの単位が「あの依頼から」だからで
// ある。モデルの発言の途中を起点にできても、消したあとに何を送ればよいかが
// 決まらない。委譲された子の発言も、親より後の連番を持つのでまとめて消える。
func (s *Store) Rewind(ctx context.Context, sessionID, messageID string) (int, string, error) {
	var seq int64
	var role, content, parentID string
	err := s.db.QueryRowContext(ctx,
		`SELECT seq, role, content, parent_id FROM messages WHERE id = ? AND session_id = ?`,
		messageID, sessionID).Scan(&seq, &role, &content, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	if err != nil {
		return 0, "", err
	}
	if role != provider.RoleUser || parentID != "" {
		return 0, "", ErrNotRewindable
	}

	res, err := s.db.ExecContext(ctx,
		`DELETE FROM messages WHERE session_id = ? AND seq >= ?`, sessionID, seq)
	if err != nil {
		return 0, "", err
	}
	n, _ := res.RowsAffected()

	// 実測値が無くなったので使用量は不明に戻す。前のターンの値を残すと、
	// 消したはずの分を数えたままの割合が出る。
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET context_tokens = 0, context_limit = 0, updated_at = ? WHERE id = ?`,
		time.Now().UnixMilli(), sessionID); err != nil {
		return 0, "", err
	}
	return int(n), content, nil
}
