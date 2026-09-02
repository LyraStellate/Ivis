package tools

import (
	"context"
	"fmt"
)

type delegateTool struct{}

func (t *delegateTool) Name() string { return "delegate" }
func (t *delegateTool) Description() string {
	return "別のエージェントに仕事を任せ、その成果だけを受け取る。任せられる相手は指示文に列挙されている。"
}
func (t *delegateTool) Parameters() map[string]any {
	return schema(map[string]any{
		"agent": strProp("任せる相手のエージェント ID。"),
		"task":  strProp("依頼内容。相手はこの会話の文脈を見られないため、必要な前提を含めて書くこと。"),
	}, "agent", "task")
}

// 委譲そのものは副作用を持たない。子が使うツールは子の側で承認を求める。
func (t *delegateTool) NeedsApproval() bool { return false }

func (t *delegateTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	agentID, err := argString(args, "agent")
	if err != nil {
		return "", err
	}
	task, err := argString(args, "task")
	if err != nil {
		return "", err
	}
	if ec.Delegate == nil {
		return "", fmt.Errorf("このセッションでは委譲を実行できません")
	}
	// 呼べる相手は定義に列挙されたものに限る。ここを緩めると、定義を読んだ
	// だけでは何が起きうるか分からなくなる。
	if ec.CanDelegateTo == nil || !ec.CanDelegateTo(agentID) {
		return "", fmt.Errorf("エージェント %q への委譲は許可されていません", agentID)
	}
	return ec.Delegate(ctx, agentID, task)
}
