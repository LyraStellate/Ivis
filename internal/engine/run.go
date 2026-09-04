package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
)

// runCtx は 1 つの実行ループの状態。委譲した子も同じ構造で走る。
type runCtx struct {
	sessionID string
	agent     *agent.Agent
	// parentID が空でなければ、この実行は委譲された子である。発言は親の
	// メッセージにぶら下げて保存し、親の会話には混ぜない。
	parentID string
	depth    int
	msgs     []provider.Message
	emit     Emit
}

// Run は利用者の入力を 1 ターン処理する。
func (e *Engine) Run(ctx context.Context, sessionID, userText string, emit Emit) error {
	sess, err := e.Store.GetSession(ctx, sessionID)
	if err != nil {
		return err
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
		agent:     ag,
		depth:     0,
		msgs:      toProviderMessages(history),
		emit:      emit,
	}
	_, err = e.loop(ctx, rc)
	if err == nil {
		emit(Event{Type: EvtDone})
	}
	return err
}

// loop はツール呼び出しが無くなるまで生成を繰り返す。最後の本文を返す。
func (e *Engine) loop(ctx context.Context, rc *runCtx) (string, error) {
	sys := systemPrompt(rc.agent, e.Skills.Filter(rc.agent.Skills), e.delegateAgents(rc.agent),
		e.Cfg.SessionWorkspace(rc.sessionID), time.Now())
	defs := e.Tools.Defs(rc.agent.Allows)

	var last string
	for iter := 0; iter < e.Cfg.MaxIterations; iter++ {
		req := provider.Request{
			Model:    rc.agent.Model,
			Messages: append([]provider.Message{{Role: provider.RoleSystem, Content: sys}}, rc.msgs...),
			Tools:    defs,
			Options:  rc.agent.Options,
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
func (e *Engine) delegateAgents(a *agent.Agent) []*agent.Agent {
	if !a.Allows("delegate") {
		return nil
	}
	return e.Agents.Below(a)
}
