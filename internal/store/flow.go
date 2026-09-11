package store

import "context"

// FlowEdge はチームセッションの矢印 1 本の生データ (#512740)。
//
// 矢印は新しい表ではなく messages に乗っている。role='team' の行が持つ
// agent_id (送り手) と to_agent_id (宛先) が、そのまま 1 本の矢印である。
// 利用者の発言 (role='user' + to_agent_id) が起点になる。
//
// 別の表を持たないのは、巻き戻し・/clear・会話の削除がどれも messages を
// 消すことで矢印まで片付くからである。表を分けると、その 3 か所すべてに
// 掃除を足すことになり、1 つ忘れれば存在しないやり取りの矢印だけが残る。
type FlowEdge struct {
	ID   string
	Seq  int64
	From string
	To   string
	Body string
	// Decision は依頼への返答のときだけ、受諾か却下か。
	Decision string
	// ReplyTo はこの 1 通が応えた相手の ID。閉じているかはこれで決まり、
	// 何ターンめかは、ここを何本たどれるかで決まる。
	ReplyTo string
}

// TeamFlow は会話の矢印を古い順に返す。
//
// 要約より前を切り落とさない。圧縮 (#486237) で会話が畳まれても、誰が誰に
// 何を頼んだかは残らなければならない — 畳まれて消えることが、そもそも
// この記録を持つ理由である。
func (s *Store) TeamFlow(ctx context.Context, sessionID string) ([]*FlowEdge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, seq, agent_id, to_agent_id, content, decision, reply_to
		   FROM messages
		  WHERE session_id = ? AND parent_id = '' AND to_agent_id <> ''
		    AND role IN (?, ?)
		  ORDER BY seq ASC`,
		sessionID, RoleTeam, "user")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*FlowEdge
	for rows.Next() {
		var e FlowEdge
		if err := rows.Scan(&e.ID, &e.Seq, &e.From, &e.To, &e.Body,
			&e.Decision, &e.ReplyTo); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
