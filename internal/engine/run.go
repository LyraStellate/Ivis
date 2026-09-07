package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/team"
	"github.com/LyraStellate/Ivis/internal/tools"
)

// runCtx は 1 つの実行ループの状態。委譲した子も、チームの手番も同じ構造で走る。
type runCtx struct {
	sessionID string
	// kind は会話の進み方。作業ディレクトリの場所と、渡すツールの範囲が
	// これで決まる (#731906)。
	kind  string
	agent *agent.Agent
	// parentID が空でなければ、この実行は委譲された子である。発言は親の
	// メッセージにぶら下げて保存し、親の会話には混ぜない。
	parentID string
	depth    int
	msgs     []provider.Message
	emit     Emit

	// ここから下はチームセッションの手番だけが持つ (#640275)。
	//
	// roster はこの会話の名簿。誰に何を送れるかはここから決まる。
	roster *team.Roster
	// requester はこの手番を始めさせた依頼の送り主。依頼でなければ空で、
	// ここへ返すときは可否を添えなければならない。
	requester string
	// send は 1 通送る。engine が保存と行列への積み込みを行う。
	send func(ctx context.Context, msg tools.TeamMessage) error
}

// team はチームの手番かどうか。
func (rc *runCtx) team() bool { return rc.roster != nil }

// Run は利用者の入力を 1 ターン処理する。
func (e *Engine) Run(ctx context.Context, sessionID, userText string, emit Emit) error {
	sess, err := e.Store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	// チームは進み方が違う。1 体の実行ループではなく、手番を回す。
	if sess.Kind == config.KindTeam {
		return e.runTeam(ctx, sess, userText, emit)
	}
	ag, ok := e.Agents.Get(sess.AgentID)
	if !ok {
		return fmt.Errorf("エージェント %q の定義が見つかりません。定義ファイルが削除されたか、読み込みに失敗しています", sess.AgentID)
	}

	userMsg := &store.Message{
		SessionID: sessionID,
		Role:      provider.RoleUser,
		Content:   userText,
		AgentID:   ag.ID,
	}
	if err := e.Store.AppendMessage(ctx, userMsg); err != nil {
		return fmt.Errorf("入力を保存できませんでした: %w", err)
	}
	// 画面は送った本文を先に自前で置く。保存された識別子を返しておかないと、
	// その発言を指す操作 (巻き戻し) が、開き直すまでできない。
	emit(Event{Type: EvtUserSaved, MessageID: userMsg.ID})
	// 保存されていないのに保存されたように見える状態を作らないため、
	// 履歴の書き込み失敗はここで返す。
	e.maybeSetTitle(ctx, sess, userText)

	history, err := e.Store.ConversationMessages(ctx, sessionID)
	if err != nil {
		return err
	}

	rc := &runCtx{
		sessionID: sessionID,
		kind:      sess.Kind,
		agent:     ag,
		depth:     0,
		msgs:      toProviderMessages(history),
		emit:      emit,
	}
	_, err = e.loop(ctx, rc)
	if err != nil {
		return err
	}
	// 上限に近づいていれば、次のターンを始める前にまとめておく。答えを出して
	// からにするのは、待たされる場所を 1 か所へ寄せるためである (#486237)。
	e.maybeCompact(ctx, sessionID, emit)
	emit(Event{Type: EvtDone})
	return nil
}

// loop はツール呼び出しが無くなるまで生成を繰り返す。最後の本文を返す。
func (e *Engine) loop(ctx context.Context, rc *runCtx) (string, error) {
	sys := systemPrompt(rc.agent, e.Skills.Filter(rc.agent.Skills), e.delegateAgents(rc),
		e.Cfg.SessionWorkspace(rc.kind, rc.sessionID), time.Now(), e.teamNote(ctx, rc))
	defs := e.Tools.Defs(e.allows(rc), rc.team())
	opts := e.options(ctx, rc.agent)

	var last string
	for iter := 0; iter < e.Cfg.MaxIterations; iter++ {
		req := provider.Request{
			Model:    rc.agent.Model,
			Messages: append([]provider.Message{{Role: provider.RoleSystem, Content: sys}}, rc.msgs...),
			Tools:    defs,
			Options:  opts,
			Think:    rc.agent.Thinking,
		}

		msgID, text, calls, err := e.generate(ctx, rc, req)
		if err != nil {
			return last, err
		}
		last = text
		rc.msgs = append(rc.msgs, provider.Message{
			Role: provider.RoleAssistant, Content: text, ToolCalls: calls,
		})
		if len(calls) == 0 {
			return last, nil
		}
		if err := e.runToolCalls(ctx, rc, msgID, calls); err != nil {
			return last, err
		}
	}

	// ローカルモデルは同じツールを呼び続けるループに入ることがある。
	// 打ち切ったうえで、理由を利用者に示す。
	msg := fmt.Sprintf("ツール呼び出しが %d 回に達したため打ち切りました。", e.Cfg.MaxIterations)
	rc.emit(Event{Type: EvtError, Depth: rc.depth, AgentID: rc.agent.ID, Error: msg})
	return last, reported(errors.New(msg))
}

// options はモデルへ渡す生成パラメータを組み立てる。
//
// 定義のものをそのまま渡さず写しを作るのは、ここで足すコンテキスト長が定義へ
// 書き戻ってしまわないようにするためである。定義はファイルが正であり、
// 実行の都合で決めた値がそこに混ざってはならない。
func (e *Engine) options(ctx context.Context, a *agent.Agent) map[string]any {
	opts := make(map[string]any, len(a.Options)+1)
	for k, v := range a.Options {
		opts[k] = v
	}
	if _, ok := opts["num_ctx"]; !ok {
		opts["num_ctx"] = e.numCtx(ctx, a)
	}
	return opts
}

// generate は 1 回の生成を行い、保存した発言の識別子・本文・ツール呼び出しを返す。
// 識別子はツール呼び出しに一意な名前を与えるために使う。
func (e *Engine) generate(ctx context.Context, rc *runCtx, req provider.Request) (string, string, []provider.ToolCall, error) {
	stream, err := e.Provider.Chat(ctx, req)
	if err != nil {
		rc.emit(Event{Type: EvtError, Depth: rc.depth, AgentID: rc.agent.ID,
			Error: err.Error(), Kind: KindOf(err)})
		return "", "", nil, reported(err)
	}

	msg := &store.Message{
		SessionID: rc.sessionID,
		ParentID:  rc.parentID,
		Role:      provider.RoleAssistant,
		AgentID:   rc.agent.ID,
		Model:     rc.agent.Model,
	}
	if err := e.Store.AppendMessage(ctx, msg); err != nil {
		return "", "", nil, fmt.Errorf("応答を保存できませんでした: %w", err)
	}
	rc.emit(Event{Type: EvtMessageStart, MessageID: msg.ID, AgentID: rc.agent.ID,
		ParentID: rc.parentID, Depth: rc.depth})

	var sb strings.Builder
	// 推論の過程は本文と別に溜める。混ぜると、後から読み返したときに結論と
	// 過程の区別がつかなくなる。
	var think strings.Builder
	var calls []provider.ToolCall
	var genErr error
	// cut はコンテキストが尽きて生成が打ち切られたこと。黙って途中で終わると、
	// 利用者にはモデルが答え終えたように見える。
	cut := false

	// 途中経過を一定間隔で書き出す。プロセスが落ちてもここまでは残る。
	flush := time.NewTicker(700 * time.Millisecond)
	defer flush.Stop()
	dirty := false

	persist := func(errText string) {
		if err := e.Store.UpdateMessage(ctx, msg.ID, sb.String(), think.String(), errText, calls); err != nil {
			rc.emit(Event{Type: EvtError, Depth: rc.depth, Error: "履歴の書き込みに失敗しました: " + err.Error()})
		}
	}

loop:
	for {
		select {
		case <-flush.C:
			if dirty {
				persist("")
				dirty = false
			}
		case ev, ok := <-stream:
			if !ok {
				break loop
			}
			switch ev.Type {
			case provider.EventThinking:
				think.WriteString(ev.Text)
				dirty = true
				rc.emit(Event{Type: EvtThinking, MessageID: msg.ID, Depth: rc.depth, Text: ev.Text})
			case provider.EventDelta:
				sb.WriteString(ev.Text)
				dirty = true
				rc.emit(Event{Type: EvtDelta, MessageID: msg.ID, Depth: rc.depth, Text: ev.Text})
			case provider.EventToolCalls:
				calls = ev.ToolCalls
			case provider.EventError:
				genErr = ev.Err
				break loop
			case provider.EventDone:
				e.noteUsage(ctx, rc, ev.Usage)
				cut = ev.Truncated
				break loop
			}
		}
	}

	if genErr != nil {
		// 部分出力は保存したうえでエラーとして示す。続きは再試行できる。
		persist(genErr.Error())
		rc.emit(Event{Type: EvtError, MessageID: msg.ID, Depth: rc.depth,
			AgentID: rc.agent.ID, Error: genErr.Error(), Kind: KindOf(genErr)})
		return msg.ID, sb.String(), nil, reported(genErr)
	}
	if cut {
		// 失敗ではないが、続きがある。何が起きたかと、次に何をすれば
		// よいかを添える。
		note := fmt.Sprintf("コンテキストの上限 (%d) に達したため、ここで打ち切られました。"+
			"続きが要るなら、設定のコンテキスト長を増やすか、会話を分けてください。",
			e.contextLimit(ctx, rc.agent))
		persist(note)
		rc.emit(Event{Type: EvtError, MessageID: msg.ID, Depth: rc.depth,
			AgentID: rc.agent.ID, Error: note})
		return msg.ID, sb.String(), calls, nil
	}
	persist("")
	rc.emit(Event{Type: EvtMessageEnd, MessageID: msg.ID, Depth: rc.depth})
	return msg.ID, sb.String(), calls, nil
}

func (e *Engine) maybeSetTitle(ctx context.Context, sess *store.Session, userText string) {
	if sess.Title != "新しい会話" {
		return
	}
	title := oneLine(userText, 30)
	if title == "" {
		return
	}
	_ = e.Store.UpdateSession(ctx, sess.ID, title, "")
}

// delegateAgents は指示文へ載せる委譲先を返す。誰を呼べるかは Tier だけで
// 決まるため、定義に呼び先を列挙する必要はない。
//
// チームでは空を返す。委譲そのものを渡さないので、載せると呼べないものの
// 一覧になる (#640275)。
func (e *Engine) delegateAgents(rc *runCtx) []*agent.Agent {
	if rc.team() || !e.allows(rc)("delegate") {
		return nil
	}
	return e.Agents.Below(rc.agent)
}

// allows はこの実行で使ってよいツールかを返す。
//
// チームでは委譲を外す。委譲とメンションは、どちらも仕事を人に渡す手段で
// ありながら、片方は記録に残って全員が指せる名前を持ち、もう片方は呼んだ
// 本人にしか見えないまま消える。両方あると、会話の記録が「誰が何をしたか」の
// 記録として信用できなくなる (#640275)。
func (e *Engine) allows(rc *runCtx) func(string) bool {
	return func(name string) bool {
		if rc.team() && name == "delegate" {
			return false
		}
		return rc.agent.Allows(name)
	}
}
