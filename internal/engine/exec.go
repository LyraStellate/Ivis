package engine

import (
	"context"
	"fmt"
	"time"

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

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func (e *Engine) runOneTool(ctx context.Context, rc *runCtx, callID string, call provider.ToolCall) (string, error) {
	tool, ok := e.Tools.Get(call.Name)
	if !ok {
		return "", fmt.Errorf("ツール %q は存在しません", call.Name)
	}
	if !rc.agent.Allows(call.Name) {
		return "", fmt.Errorf("エージェント %q にツール %q の使用は許可されていません", rc.agent.ID, call.Name)
	}

	if tool.NeedsApproval() && e.Cfg.RequireApproval && e.Approver != nil {
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
		Workspace:     e.Cfg.WorkspaceDir,
		Skills:        e.Skills,
		ScriptTimeout: time.Duration(e.Cfg.ScriptTimeoutSec) * time.Second,
		AgentID:       rc.agent.ID,
		CanDelegateTo: rc.agent.CanDelegateTo,
		Delegate: func(ctx context.Context, agentID, task string) (string, error) {
			// 呼び出しの識別子を渡し、委譲の開始と終了もその呼び出しに
			// 結び付けられるようにする。
			return e.delegate(ctx, rc, callID, agentID, task)
		},
	}
	return tool.Execute(ctx, ec, call.Arguments)
}

// delegate は子エージェントを独立した文脈で走らせ、その成果だけを親へ返す。
//
// 子の途中経過は親の文脈を圧迫しないよう戻さない。履歴には親メッセージに
// ぶら下げて保存するため、UI からは追える。
func (e *Engine) delegate(ctx context.Context, parent *runCtx, callID, agentID, task string) (string, error) {
	if parent.depth+1 > e.Cfg.MaxDelegationDepth {
		return "", fmt.Errorf("委譲の深さが上限 (%d) に達しました", e.Cfg.MaxDelegationDepth)
	}
	for _, id := range parent.stack {
		if id == agentID {
			return "", fmt.Errorf("委譲が循環しています (%s)", agentID)
		}
	}
	child, ok := e.Agents.Get(agentID)
	if !ok {
		return "", fmt.Errorf("エージェント %q の定義が見つかりません", agentID)
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
		stack:     append(append([]string{}, parent.stack...), child.ID),
		msgs:      []provider.Message{{Role: provider.RoleUser, Content: task}},
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
