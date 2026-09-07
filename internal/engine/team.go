package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/team"
	"github.com/LyraStellate/Ivis/internal/tools"
)

// チームセッションの 1 ラウンド (#640275)。
//
// 利用者の 1 回の送信が 1 ラウンドである。宛先のメンバーが手番を取り、その
// 手番で送られたメッセージが次の手番になる。行列が空になったらラウンドが
// 終わる。
//
// 手番は 1 つずつ回す。作業ディレクトリは会話に 1 つで、同時に書けば同じ
// ファイルの結果が不定になる。承認も同時に 2 件出れば、画面のどちらに答えて
// いるのか分からない。実装名の parallel は「複数体が 1 つの会話に居る形態」の
// 呼び名であって、同時に走ることの約束ではない (#258413)。

// turn は行列に積まれた 1 手番。
type turn struct {
	// From は送り手。利用者からなら空。
	From string
	To   string
	Body string
	Why  string
	Did  string
	// Rel は From から To への関係。依頼なら、返答に可否が要る。
	Rel string
}

// runTeam はチームセッションの 1 ラウンドを回す。
func (e *Engine) runTeam(ctx context.Context, sess *store.Session, userText string, emit Emit) error {
	roster := team.Load(e.Cfg, e.Agents, sess)
	for _, le := range roster.Errors {
		// 1 体読めなくてもラウンドは回す。誰が欠けているかだけは伝える。
		emit(Event{Type: EvtNotice, Text: le.Reason})
	}
	if len(roster.Members) == 0 {
		return fmt.Errorf("この会話にはメンバーが 1 人も居ません。右のパネルから足してください")
	}
	if _, ok := roster.Lead(); !ok {
		return fmt.Errorf("窓口 %q がこの会話に居ません。窓口を選び直してください", roster.LeadID)
	}

	to, body := team.ParseMention(userText)
	if to == "" || to == team.Anyone {
		// 宛先の無い依頼と "*" は窓口へ届く。宛先を決める判断は、名簿を
		// 持っている窓口が行う。
		to = roster.LeadID
	} else if _, ok := roster.Get(to); !ok {
		return fmt.Errorf("%q はこの会話に居ません。居るのは %s です",
			to, strings.Join(roster.IDs(), ", "))
	}

	userMsg := &store.Message{
		SessionID: sess.ID,
		Role:      provider.RoleUser,
		Content:   body,
		ToAgentID: to,
	}
	if err := e.Store.AppendMessage(ctx, userMsg); err != nil {
		return fmt.Errorf("入力を保存できませんでした: %w", err)
	}
	emit(Event{Type: EvtUserSaved, MessageID: userMsg.ID, AgentID: to})
	e.maybeSetTitle(ctx, sess, body)

	queue := []turn{{To: to, Body: body}}
	done := 0

	for len(queue) > 0 {
		if done >= e.Cfg.MaxTurns {
			// 打ち切りは失敗ではない。ここまでの結果と、残っている行列を
			// そのまま見せる。何が積まれたままかを隠すと、続きを指示し
			// 直すこともできない。
			emit(Event{Type: EvtNotice, Text: fmt.Sprintf(
				"手番が上限 (%d) に達したので、ここで止めました。残っている宛先: %s",
				e.Cfg.MaxTurns, waitingFor(queue))})
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		t := queue[0]
		queue = queue[1:]
		done++

		// 名簿は手番ごとに読み直す。待つ間に外された相手へ回しても、走らせる
		// 定義が無い。会話の行から読み直すのは、参加の変更がそこにあるため
		// である。手元の写しを使うと、外したはずの相手が走り続ける。
		if cur, err := e.Store.GetSession(ctx, sess.ID); err == nil {
			sess = cur
		}
		roster = team.Load(e.Cfg, e.Agents, sess)
		m, ok := roster.Get(t.To)
		if !ok {
			emit(Event{Type: EvtNotice, Text: fmt.Sprintf(
				"%s へのメッセージは届けられませんでした。もうこの会話に居ません。", t.To)})
			continue
		}

		emit(Event{Type: EvtTurnStart, AgentID: m.ID, Text: t.From,
			Relation: t.Rel, Queued: len(queue)})

		sent, err := e.runTurn(ctx, sess, roster, m.ID, t, emit)
		queue = append(queue, sent...)

		emit(Event{Type: EvtTurnEnd, AgentID: m.ID, Queued: len(queue)})

		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// 1 人の失敗でラウンドを止めない。長いラウンドが最後の 1 人で
			// 無に帰する。依頼元には失敗したことを返し、次へ進む。
			if !Reported(err) {
				emit(Event{Type: EvtError, AgentID: m.ID, Error: err.Error(), Kind: KindOf(err)})
			}
			if t.From != "" {
				queue = append(queue, turn{
					From: m.ID, To: t.From, Rel: roster.Relation(m.ID, t.From),
					Why:  fmt.Sprintf("%s から受けた仕事の途中で失敗しました。", t.From),
					Did:  "手番が最後まで進みませんでした。",
					Body: "実行が失敗しました: " + err.Error(),
				})
			}
		}
	}

	e.maybeCompact(ctx, sess.ID, emit)
	emit(Event{Type: EvtDone})
	return nil
}

// waitingFor は残っている宛先を並べる。
func waitingFor(queue []turn) string {
	if len(queue) == 0 {
		return "なし"
	}
	var to []string
	for _, t := range queue {
		to = append(to, t.To)
	}
	return strings.Join(to, ", ")
}

// runTurn は 1 人の手番を回し、その手番で送られたメッセージを返す。
func (e *Engine) runTurn(ctx context.Context, sess *store.Session, roster *team.Roster,
	self string, in turn, emit Emit) ([]turn, error) {
	m, _ := roster.Get(self)

	history, err := e.Store.TeamMessages(ctx, sess.ID, self)
	if err != nil {
		return nil, err
	}

	var sent []turn
	rc := &runCtx{
		sessionID: sess.ID,
		kind:      sess.Kind,
		agent:     m.Agent,
		roster:    roster,
		msgs:      toTeamMessages(history, roster, self),
		emit:      emit,
	}
	// 依頼への返答には可否が要る。誰の依頼だったかは、この手番を始めさせた
	// メッセージにしか書かれていない。
	if in.Rel == team.RelRequest && in.From != "" {
		rc.requester = in.From
	}
	rc.send = func(ctx context.Context, msg tools.TeamMessage) error {
		saved, err := e.saveTeamMessage(ctx, sess.ID, self, roster, msg, emit)
		if err != nil {
			return err
		}
		if msg.To != team.Anyone {
			sent = append(sent, saved)
		}
		return nil
	}

	_, err = e.loop(ctx, rc)
	return sent, err
}

// saveTeamMessage は 1 通を記録し、画面へ流し、次の手番の形にして返す。
func (e *Engine) saveTeamMessage(ctx context.Context, sessionID, from string,
	roster *team.Roster, msg tools.TeamMessage, emit Emit) (turn, error) {
	rel := roster.Relation(from, msg.To)
	rec := &store.Message{
		SessionID: sessionID,
		Role:      store.RoleTeam,
		Content:   msg.Body,
		AgentID:   from,
		ToAgentID: msg.To,
		Why:       msg.Why,
		Did:       msg.Did,
		Decision:  msg.Decision,
	}
	if err := e.Store.AppendMessage(ctx, rec); err != nil {
		return turn{}, fmt.Errorf("メッセージを保存できませんでした: %w", err)
	}
	emit(Event{Type: EvtTeamMessage, MessageID: rec.ID, AgentID: rec.AgentID,
		To: rec.ToAgentID, Text: rec.Content, Why: rec.Why, Did: rec.Did,
		Decision: rec.Decision, Relation: rel})
	return turn{From: from, To: msg.To, Body: msg.Body, Why: msg.Why, Did: msg.Did, Rel: rel}, nil
}

// toTeamMessages はそのメンバーの入力を組み立てる。
//
// 自分に宛てられたメッセージには、誰から来たのか、それが報告か依頼か指示か、
// そして「なぜ」「やったこと」を添える。添えないと、受け手は本文だけを見て、
// 誰の依頼で何の続きなのか分からない — メンバーはこの会話のほかのやり取りを
// 読めないのだから、ここに書かれていないことは存在しないのと同じである。
func toTeamMessages(history []*store.Message, roster *team.Roster, self string) []provider.Message {
	out := make([]provider.Message, 0, len(history))
	for _, m := range history {
		switch m.Role {
		case store.RoleSummary:
			out = append(out, provider.Message{
				Role: provider.RoleSystem, Content: summaryHeader + m.Content})
		case provider.RoleUser:
			out = append(out, provider.Message{
				Role: provider.RoleUser, Content: "利用者からの依頼:\n" + m.Content})
		case store.RoleTeam:
			out = append(out, provider.Message{
				Role: provider.RoleUser, Content: incomingText(m, roster.Relation(m.AgentID, self))})
		case provider.RoleAssistant, provider.RoleTool:
			out = append(out, provider.Message{
				Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls, ToolName: m.ToolName})
		}
	}
	return out
}

// incomingText は届いた 1 通を、受け手が読む形にする。
func incomingText(m *store.Message, rel string) string {
	var b strings.Builder
	if rel == "" {
		rel = "連絡"
	}
	fmt.Fprintf(&b, "%s からの%s", m.AgentID, rel)
	if m.Decision != "" {
		fmt.Fprintf(&b, " (%s)", m.Decision)
	}
	b.WriteString("\n")
	if m.Why != "" {
		b.WriteString("経緯: " + m.Why + "\n")
	}
	if m.Did != "" {
		b.WriteString("相手がやったこと: " + m.Did + "\n")
	}
	b.WriteString("\n" + m.Content)
	return b.String()
}

// teamContext はツールへ渡す手番 1 回分を組み立てる。
func teamContext(rc *runCtx) *tools.TeamContext {
	if rc.roster == nil {
		return nil
	}
	members := make([]tools.TeamMember, 0, len(rc.roster.Members))
	for _, m := range rc.roster.Members {
		members = append(members, tools.TeamMember{
			ID: m.ID, Name: m.Name, Relation: rc.roster.Relation(rc.agent.ID, m.ID)})
	}
	return &tools.TeamContext{
		Self:        rc.agent.ID,
		LeadID:      rc.roster.LeadID,
		Members:     members,
		RequesterID: rc.requester,
		Anyone:      team.Anyone,
		AcceptWord:  team.DecisionAccept,
		RejectWord:  team.DecisionReject,
		Send:        rc.send,
	}
}

// teamNote は指示文へ足すチーム向けの一式。名簿・進め方・自分の担当チケット。
func (e *Engine) teamNote(ctx context.Context, rc *runCtx) string {
	if rc.roster == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(rc.roster.Roll(rc.agent.ID))
	b.WriteString(team.Guide(rc.agent.ID, rc.agent.ID == rc.roster.LeadID))
	b.WriteString(e.ticketNote(ctx, rc))
	return b.String()
}

// ticketNote は自分が担当で終わっていないチケットを載せる。
//
// 毎ターン list_tickets を呼ばせるのは往復の無駄で、呼び忘れれば自分の担当を
// 放置する。見えていないものは扱われない (#189542)。
func (e *Engine) ticketNote(ctx context.Context, rc *runCtx) string {
	if !rc.agent.Allows("list_tickets") && !rc.agent.Allows("update_ticket") {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n仕事はチケットで持ちます。ほかのメンバーのやり取りは読めないので、" +
		"誰が何をどこまで進めたかはチケットにしか残りません。\n")
	b.WriteString("- 仕事を受けたら起票するか、既にある番号を確かめてください。\n")
	b.WriteString("- 着手したら状態を進行中に、終えたら解決かレビューにしてください。\n")
	b.WriteString("- 判断したこと、試して駄目だったことは注記に残してください。\n")

	list, err := e.Store.ListTickets(ctx, rc.sessionID,
		store.TicketFilter{Assignee: rc.agent.ID})
	if err != nil {
		return b.String()
	}
	if len(list) == 0 {
		b.WriteString("\nあなたが担当しているチケットはいまありません。\n")
		return b.String()
	}
	b.WriteString("\nあなたが担当している、終わっていないチケット:\n")
	b.WriteString(tools.FormatTicketList(list) + "\n")
	return b.String()
}
