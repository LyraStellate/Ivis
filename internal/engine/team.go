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
//
// 1 通が 1 手番である。同じ相手へ 2 通来れば、その相手は 2 回動く。頼んだ
// 2 人それぞれに返事が返るためで、まとめて 1 回にすると片方が返事を受け
// 取れない (#512740)。
type turn struct {
	// From は送り手。利用者からなら空。
	From string
	To   string
	Body string
	Why  string
	Did  string
	// MsgID はこの 1 通の記録上の ID。応えたときに reply_to へ入れる。
	MsgID string
	// N は何ターンめか。種を 1 とし、そこから何本たどったかを数える。
	// 図の列と同じ数え方で、画面の帯に出す (#512740)。
	N int
	// Rel は From から To への関係。依頼なら、返答に可否が要る。
	Rel string
	// auto はこの手番が、engine が繕った返信から始まったこと。繕いから
	// 始まった手番をさらに繕うと、何も進まないまま往復し続ける (#640275)。
	auto bool
}

// AutoNote は engine が繕った返信に付ける印。人にもモデルにも、これが本人の
// 言葉でないことが分かるようにする。
const AutoNote = "(自動)"

// runTeam はチームセッションの 1 ラウンドを回す。
func (e *Engine) runTeam(ctx context.Context, sess *store.Session, userText string, emit Emit) error {
	roster := team.Load(e.Agents, e.TeamAgents, sess)
	for _, le := range roster.Errors {
		// 1 体読めなくてもラウンドは回す。誰が欠けているかだけは伝える。
		emit(Event{Type: EvtNotice, Text: le.Reason})
	}
	if len(roster.Members) == 0 {
		return fmt.Errorf("この会話にはメンバーが 1 人も居ません。右のパネルから足してください")
	}

	to, body := team.ParseMention(userText)
	switch {
	case to != "":
		if _, ok := roster.Get(to); !ok {
			return fmt.Errorf("%q はこの会話に居ません。居るのは %s です",
				to, strings.Join(roster.IDs(), ", "))
		}
	case len(roster.Members) == 1:
		// 迷う余地が無い。1 人しか居ないのに宛先を書かせる理由は無い。
		to = roster.Members[0].ID
	default:
		// ここで誰かを選ぶと、それは窓口を別の名前で復活させたことになる。
		// 断る位置は、発言を保存する前でなければならない — 保存してから
		// 断ると、宛先の無い発言が履歴に溜まる。
		return fmt.Errorf("%w。誰に頼むかを @ で指定してください。この会話に居るのは %s です",
			ErrNoAddressee, strings.Join(roster.IDs(), ", "))
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

	queue := []turn{{To: to, Body: body, MsgID: userMsg.ID, N: 1}}
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

		// 素の先入れ先出しは、そのまま幅優先である。あるターンで積まれた分は
		// どれも、次のターンの分より前に出てくる — ターンという区切りを実行側
		// が持つ必要はなく、行列に入れた順がそれを表している (#512740)。
		t := queue[0]
		queue = queue[1:]
		done++

		// 名簿は手番ごとに読み直す。待つ間に外された相手へ回しても、走らせる
		// 定義が無い。会話の行から読み直すのは、参加の変更がそこにあるため
		// である。手元の写しを使うと、外したはずの相手が走り続ける。
		if cur, err := e.Store.GetSession(ctx, sess.ID); err == nil {
			sess = cur
		}
		roster = team.Load(e.Agents, e.TeamAgents, sess)
		m, ok := roster.Get(t.To)
		if !ok {
			emit(Event{Type: EvtNotice, Text: fmt.Sprintf(
				"%s へのメッセージは届けられませんでした。もうこの会話に居ません。", t.To)})
			continue
		}

		emit(Event{Type: EvtTurnStart, AgentID: m.ID, Text: t.From,
			Relation: t.Rel, Queued: len(queue), Turn: t.N})

		sent, last, err := e.runTurn(ctx, sess, roster, m.ID, t, emit)
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
			if next, ok := e.fillIn(ctx, sess, roster, m.ID, t,
				fmt.Sprintf("%s から受けた仕事の途中で失敗しました。", t.From),
				"手番が最後まで進みませんでした。",
				"実行が失敗しました: "+err.Error(), emit); ok {
				queue = append(queue, next)
			}
			continue
		}

		// 促しても誰にも渡さずに終わった手番。上に立つ相手がそうしたなら、
		// 仕事は誰にも配られていない。ラウンドはそこで終わるので、利用者に
		// は「何も起きなかった」ようにしか見えない。理由を出す。
		if len(sent) == 0 && t.From == "" && len(roster.Subordinates(m.ID)) > 0 {
			emit(Event{Type: EvtNotice, Text: fmt.Sprintf(
				"%s は誰にも仕事を渡さずに手番を終えました。指示が必要なら、"+
					"宛先を書いてもう一度送ってください。", m.ID)})
		}

		// 誰にも渡さずに終わった手番。依頼した側は、返事が来ないまま待ち
		// 続けることになる。ラウンドはそこで空になって終わり、頼んだ仕事が
		// どうなったのかは誰にも分からない。こちらで返す (#640275)。
		if len(sent) == 0 {
			body := strings.TrimSpace(last)
			if body == "" {
				body = "この手番からは何も返りませんでした。同じ頼み方では進まないので、" +
					"内容を分けるか、別の相手に頼んでください。"
			}
			if next, ok := e.fillIn(ctx, sess, roster, m.ID, t,
				fmt.Sprintf("%s AI が返信を送らないまま手番を終えたため、Ivis が代わりに返しています。", AutoNote),
				"", body, emit); ok {
				queue = append(queue, next)
			}
		}
	}

	e.maybeCompact(ctx, sess.ID, emit)
	emit(Event{Type: EvtDone})
	return nil
}

// fillIn は止まった手番の代わりに、依頼元へ 1 通返す。
//
// 返せるのは、その手番を誰かが始めた場合だけである。利用者から始まった手番が
// 何も返さないのは、ラウンドがそこで終わるというだけで、待っている相手が
// 居ない。
//
// 繕いから始まった手番はもう繕わない。繕いに繕いを返すと、何も進まないまま
// 上限まで往復する。
func (e *Engine) fillIn(ctx context.Context, sess *store.Session, roster *team.Roster,
	self string, in turn, why, did, body string, emit Emit) (turn, bool) {
	// 繕いへの返事が無いのは、ふつうの終わり方である。報告を読んで言うことが
	// 無ければ、そこで枝が閉じるのが正しい。知らせを出すと雑音になる。
	if in.From == "" || in.auto {
		return turn{}, false
	}
	emit(Event{Type: EvtNotice, Text: fmt.Sprintf(
		"%s が %s へ返信しないまま手番を終えたため、Ivis が代わりに返しました。", self, in.From)})

	next, err := e.saveTeamMessage(ctx, sess.ID, self, roster, tools.TeamMessage{
		To: in.From, Why: why, Did: did, Body: body,
	}, in.MsgID, in.N, emit)
	if err != nil {
		emit(Event{Type: EvtError, AgentID: self, Error: err.Error()})
		return turn{}, false
	}
	next.auto = true
	return next, true
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
	self string, in turn, emit Emit) ([]turn, string, error) {
	m, _ := roster.Get(self)

	history, err := e.Store.TeamMessages(ctx, sess.ID, self)
	if err != nil {
		return nil, "", err
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
		saved, err := e.saveTeamMessage(ctx, sess.ID, self, roster, msg, in.MsgID, in.N, emit)
		if err != nil {
			return err
		}
		sent = append(sent, saved)
		return nil
	}

	last, err := e.loop(ctx, rc)
	if err != nil || len(sent) > 0 {
		return sent, last, err
	}

	// 誰にも送らずに終わった。多いのは「これから誰々に頼みます」と本文へ
	// 書いて満足してしまう形で、書いた予定は誰にも届かない。1 度だけ促す。
	//
	// 促してから諦めるのは、繕った返信で代わりに答えるより、本人に送らせる
	// ほうが良いからである。促しても送らないなら、それは本当に送るものが
	// 無かったということで、そのまま終える (#640275)。
	if in.auto || len(roster.Members) < 2 {
		return sent, last, err
	}
	rc.msgs = append(rc.msgs,
		provider.Message{Role: provider.RoleAssistant, Content: last},
		provider.Message{Role: provider.RoleUser, Content: nudge})

	last2, err := e.loop(ctx, rc)
	if strings.TrimSpace(last2) != "" {
		last = last2
	}
	return sent, last, err
}

// nudge は、誰にも送らずに終えようとした手番へ 1 度だけ渡す念押し。
//
// 「送れ」とだけ書かない。送るものが本当に無い手番もあり、そこで無理に
// 送らせると、意味のないメッセージが 1 通増えて手番も 1 つ減る。どちらの
// 終わり方も正しいと明示したうえで、選ばせる。
const nudge = `いまの手番では、まだ誰にもメッセージを送っていません。

本文に「これから誰々に頼みます」と書いても、それは誰にも届きません。相手に
動いてもらうには、この手番の中で send_message を呼ぶ必要があります。予定を
書いただけで手番を終えると、そこで会話が止まります。

誰かに動いてもらう必要があるなら、いま send_message を呼んでください。
自分で答え切っていて、誰にも渡すものが無いなら、何も呼ばずにそのまま
終えてください。それも正しい終わり方です。`

// saveTeamMessage は 1 通を記録し、画面へ流し、次の手番の形にして返す。
func (e *Engine) saveTeamMessage(ctx context.Context, sessionID, from string,
	roster *team.Roster, msg tools.TeamMessage, replyTo string, n int, emit Emit) (turn, error) {
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
		ReplyTo:   replyTo,
	}
	if err := e.Store.AppendMessage(ctx, rec); err != nil {
		return turn{}, fmt.Errorf("メッセージを保存できませんでした: %w", err)
	}
	emit(Event{Type: EvtTeamMessage, MessageID: rec.ID, AgentID: rec.AgentID,
		To: rec.ToAgentID, Text: rec.Content, Why: rec.Why, Did: rec.Did,
		Decision: rec.Decision, Relation: rel})
	return turn{From: from, To: msg.To, Body: msg.Body, Why: msg.Why, Did: msg.Did,
		Rel: rel, MsgID: rec.ID, N: n + 1}, nil
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
		Members:     members,
		RequesterID: rc.requester,
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
	b.WriteString(team.Guide(rc.roster, rc.agent.ID))
	b.WriteString(e.flowNote(ctx, rc))
	b.WriteString(e.ticketNote(ctx, rc))
	return b.String()
}

// flowNote は、いま返事の返っていないやり取りを載せる (#512740)。
//
// 既に頼んであることは、頼んだ本人の履歴の中にしか無い。圧縮で要約に畳まれる
// と消え、そこで同じ相手へ同じことを重ねて頼むことになる。矢印は要約の外に
// 残るので、ここから引き直せば畳まれても残る。
//
// チケットを持たないエージェントにも出す。連絡はチケットと独立に起きる。
func (e *Engine) flowNote(ctx context.Context, rc *runCtx) string {
	edges, err := e.Store.TeamFlow(ctx, rc.sessionID)
	if err != nil {
		// 図が引けないことは、その手番の仕事が失敗したことを意味しない。
		return ""
	}
	return team.FlowNote(team.BuildFlow(rc.roster, edges), rc.agent.ID)
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
