package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
)

// runToolCalls はモデルが要求したツールを順に実行し、結果を会話へ戻す。
//
// originID は呼び出しを要求した発言の識別子。提供元が返す呼び出し ID は
// 1 回の生成の中でしか一意でなく、反復すると同じ値が再び現れる。1 ターンを
// 通して一意な名前を、発言の識別子と位置から作る。
func (e *Engine) runToolCalls(ctx context.Context, rc *runCtx, originID string, calls []provider.ToolCall) error {
	for i, call := range calls {
		callID := fmt.Sprintf("%s.%d", originID, i)

		rc.emit(Event{Type: EvtToolCall, Depth: rc.depth, AgentID: rc.agent.ID,
			Tool: call.Name, ToolCallID: callID, Args: call.Arguments})

		result, err := e.runOneTool(ctx, rc, callID, call)
		if err != nil {
			// 実行の失敗もモデルにとっては情報である。こちらで打ち切るより、
			// 内容を返して自己修正の機会を与える。
			result = "エラー: " + err.Error()
		}

		if err := e.Store.AppendMessage(ctx, &store.Message{
			SessionID: rc.sessionID,
			ParentID:  rc.parentID,
			Role:      provider.RoleTool,
			Content:   result,
			ToolName:  call.Name,
			AgentID:   rc.agent.ID,
		}); err != nil {
			return fmt.Errorf("ツールの結果を保存できませんでした: %w", err)
		}
		rc.msgs = append(rc.msgs, provider.Message{
			Role: provider.RoleTool, Content: result, ToolName: call.Name,
		})
		rc.emit(Event{Type: EvtToolResult, Depth: rc.depth, Tool: call.Name,
			ToolCallID: callID, Result: result})

		// 一覧を古くする道具だったら、そのことを伝える。伝えないと、手番が
		// 回っている間ずっと古い一覧が出たままになる。
		if err == nil {
			if what := changedBy(e, call.Name); what != "" {
				rc.emit(Event{Type: EvtChanged, Text: what})
			}
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// changedBy はその道具が古くするものの名前を返す。
func changedBy(e *Engine, name string) string {
	t, ok := e.Tools.Get(name)
	if !ok {
		return ""
	}
	return tools.Changes(t)
}

func (e *Engine) runOneTool(ctx context.Context, rc *runCtx, callID string, call provider.ToolCall) (string, error) {
	tool, ok := e.Tools.Get(call.Name)
	if !ok {
		return "", fmt.Errorf("ツール %q は存在しません", call.Name)
	}
	if !e.allows(rc)(call.Name) {
		return "", fmt.Errorf("エージェント %q にツール %q の使用は許可されていません", rc.agent.ID, call.Name)
	}
	// 渡していないものを名前で呼ばれることはある。会話の形態が合わなければ
	// ここで断る。
	if tools.IsTeamOnly(tool) && !rc.team() {
		return "", fmt.Errorf("ツール %q はチームセッションでしか使えません", call.Name)
	}

	// 確認を求めるかは、ツールの既定を設定が上書きする。判定をここ 1 か所に
	// 置くのは、ツールごとに散らすと増やしたツールで上書きを忘れるため。
	needsApproval := tool.NeedsApproval() && !e.Cfg.SkipsApproval(call.Name)
	if needsApproval && e.Cfg.RequireApproval && e.Approver != nil {
		req := ApprovalRequest{
			ID:         store.NewID(),
			SessionID:  rc.sessionID,
			AgentID:    rc.agent.ID,
			ToolCallID: callID,
			Tool:       call.Name,
			Arguments:  call.Arguments,
		}
		rc.emit(Event{Type: EvtApproval, Depth: rc.depth, Tool: call.Name,
			ToolCallID: callID, Approval: &req})

		ok, err := e.Approver.Request(ctx, req)
		if err != nil {
			return "", err
		}
		if !ok {
			// 拒否された事実もモデルへ返す。黙って何もしないと、モデルは
			// 実行されたものとして続きを進めてしまう。
			return "利用者がこの実行を拒否しました。別の方法を検討するか、理由を尋ねてください。", nil
		}
	}

	ec := &tools.ExecContext{
		// 作業場所は会話ごとに分ける。委譲された子も同じ場所を使う。子は親の
		// 依頼の一部を担うのであり、成果物の置き場を分ける理由がない。
		Workspace: e.Cfg.SessionWorkspace(rc.kind, rc.sessionID),
		// 境界を課すかはエージェントの属性。委譲しても継承しない。
		Confined:    !rc.agent.Unconfined,
		CommandIdle: time.Duration(e.Cfg.CommandIdleSec) * time.Second,
		Search:      e.Search,
		Skills:      e.Skills,
		ScriptIdle:  time.Duration(e.Cfg.ScriptIdleSec) * time.Second,
		AgentID:     rc.agent.ID,
		// 走らせたままのプロセスは会話ごとに束ねる。委譲された子も同じ会話で
		// 走るので、親が起動したものを子から読める。
		Session: rc.sessionID,
		Procs:   e.Procs,
		Ask: func(ctx context.Context, q string, choices []string) (string, error) {
			return e.ask(ctx, rc, callID, q, choices)
		},
		CheckDelegate: func(id string) error { return e.checkDelegate(rc.agent, id) },
		Delegate: func(ctx context.Context, agentID, task string) (string, error) {
			// 呼び出しの識別子を渡し、委譲の開始と終了もその呼び出しに
			// 結び付けられるようにする。
			return e.delegate(ctx, rc, callID, agentID, task)
		},
		// チームの一式。直列の会話では nil のままで、チーム専用のツールは
		// そもそもモデルへ渡らない (#640275)。
		Team:    teamContext(rc),
		Tickets: e.Store,
		Notice: func(text string) {
			rc.emit(Event{Type: EvtNotice, AgentID: rc.agent.ID, Text: text})
		},
		// 実行中の出力を、推論と同じように流す。終わるまで何も見えないと、
		// 長く走るものは止まっているのと区別が付かない (#470913)。
		Output: func(chunk string) {
			rc.emit(Event{Type: EvtToolOutput, Depth: rc.depth, AgentID: rc.agent.ID,
				Tool: call.Name, ToolCallID: callID, Text: chunk})
		},
	}
	return tool.Execute(ctx, ec, call.Arguments)
}

// ask は利用者へ問い、答えが返るまで待つ。
//
// 待っている間もターンは終わらない。答えを次のツール結果として返すことで、
// モデルは同じ思考の続きから進める。問い直しのために会話を送り直させると、
// そこまでの経過を組み立て直すことになる。
func (e *Engine) ask(ctx context.Context, rc *runCtx, callID, text string, choices []string) (string, error) {
	if e.Asker == nil {
		return "いまは利用者へ問えません。妥当な前提を自分で選んで進め、選んだ前提を答えに書き添えてください。", nil
	}
	q := Question{
		ID:         store.NewID(),
		SessionID:  rc.sessionID,
		AgentID:    rc.agent.ID,
		ToolCallID: callID,
		Text:       text,
		Choices:    choices,
	}
	rc.emit(Event{Type: EvtQuestion, Depth: rc.depth, AgentID: rc.agent.ID,
		Tool: "ask_user", ToolCallID: callID, Question: &q})

	ans, err := e.Asker.Ask(ctx, q)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(ans) == "" {
		// 空の答えは「決めてよい」と読む。ここで止めると、答えられない場面で
		// ターンが進まなくなる。
		return "答えは返りませんでした。妥当な前提を自分で選んで進め、選んだ前提を答えに書き添えてください。", nil
	}
	return ans, nil
}

// delegate は子エージェントを独立したコンテキストで走らせ、その成果だけを親へ返す。
//
// 子の途中経過は親のコンテキストを圧迫しないよう戻さない。履歴には親メッセージに
// ぶら下げて保存するため、UI からは追える。
func (e *Engine) delegate(ctx context.Context, parent *runCtx, callID, agentID, task string) (string, error) {
	if parent.depth+1 > e.Cfg.MaxDelegationDepth {
		return "", fmt.Errorf("委譲の深さが上限 (%d) に達しました", e.Cfg.MaxDelegationDepth)
	}
	if err := e.checkDelegate(parent.agent, agentID); err != nil {
		return "", err
	}
	child, _ := e.Agents.Get(agentID)

	// 引き継ぎは印を残す前に読む。これから記録する分を含めないため。
	msgs, err := e.carryOver(ctx, parent.sessionID, child, task)
	if err != nil {
		return "", err
	}

	// 委譲の起点として 1 件記録する。子の発言はこれにぶら下げる。
	marker := &store.Message{
		SessionID: parent.sessionID,
		ParentID:  parent.parentID,
		Role:      "delegate",
		Content:   task,
		AgentID:   child.ID,
	}
	if err := e.Store.AppendMessage(ctx, marker); err != nil {
		return "", fmt.Errorf("委譲を記録できませんでした: %w", err)
	}
	parent.emit(Event{Type: EvtDelegateStart, Depth: parent.depth, AgentID: child.ID,
		MessageID: marker.ID, ToolCallID: callID, Text: task})

	rc := &runCtx{
		sessionID: parent.sessionID,
		agent:     child,
		parentID:  marker.ID,
		depth:     parent.depth + 1,
		msgs:      msgs,
		emit:      parent.emit,
	}

	result, err := e.loop(ctx, rc)
	parent.emit(Event{Type: EvtDelegateEnd, Depth: parent.depth, AgentID: child.ID,
		MessageID: marker.ID, ToolCallID: callID, Result: result})
	if err != nil {
		return "", fmt.Errorf("エージェント %q の実行が失敗しました: %w", child.ID, err)
	}
	if result == "" {
		result = "(応答がありませんでした)"
	}
	return result, nil
}

// checkDelegate は委譲してよい相手かを返す。断る理由を文にして返すのは、
// これがそのままツールの結果としてモデルへ渡り、次の手を考える材料になるため。
func (e *Engine) checkDelegate(from *agent.Agent, agentID string) error {
	to, ok := e.Agents.Get(agentID)
	if !ok {
		return fmt.Errorf("エージェント %q の定義が見つかりません", agentID)
	}
	if to.ID == from.ID {
		return fmt.Errorf("自分自身には委譲できません")
	}
	if !from.CanDelegateTo(to) {
		return fmt.Errorf("エージェント %q (Tier %d) は自分 (Tier %d) より下位ではないため呼べません",
			to.ID, to.Tier, from.Tier)
	}
	return nil
}

// carryOver は子へ渡す入力を組み立てる。記憶が有効なら、同じセッションで
// 過去に受けた依頼と応答を先に置く。無効なら依頼文だけで始める。
func (e *Engine) carryOver(ctx context.Context, sessionID string, child *agent.Agent, task string) ([]provider.Message, error) {
	var msgs []provider.Message
	if child.Memory {
		past, err := e.Store.PastDelegations(ctx, sessionID, child.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range past {
			msgs = append(msgs,
				provider.Message{Role: provider.RoleUser, Content: d.Task},
				provider.Message{Role: provider.RoleAssistant, Content: d.Result})
		}
	}
	return append(msgs, provider.Message{Role: provider.RoleUser, Content: task}), nil
}
