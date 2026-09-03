// Package tools はエージェントが呼べるツールを定める。
//
// ここは engine を import しない。委譲のようにエンジン側の機能を必要とする
// ツールは、ExecContext 経由で関数を受け取る。
package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
)

// ExecContext はツール 1 回の実行に必要な文脈。
type ExecContext struct {
	// Workspace はファイル操作が越えてはならない境界。
	Workspace string
	// Skills はスキルの参照先。
	Skills *skillreg.Registry
	// ScriptTimeout はスキル同梱スクリプトの実行時間の上限。
	ScriptTimeout time.Duration
	// AgentID は呼び出し元のエージェント。
	AgentID string
	// Delegate は委譲の実行。engine が注入する。
	Delegate func(ctx context.Context, agentID, task string) (string, error)
	// CheckDelegate は委譲先として許可されているかの判定。許されないときは
	// 理由を返す。理由はそのままモデルへ渡り、次の手を考える材料になる。
	CheckDelegate func(agentID string) error
}

// Tool はモデルから呼べる 1 つの機能。
type Tool interface {
	Name() string
	Description() string
	// Parameters は JSON Schema 形式の入力定義。
	Parameters() map[string]any
	// NeedsApproval が true のツールは、実行前に利用者の承認を求める。
	NeedsApproval() bool
	Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error)
}

// Registry は利用可能なツールの集合。
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry は v1 の標準ツールを備えたレジストリを返す。
func NewRegistry() *Registry {
	r := &Registry{tools: map[string]Tool{}}
	r.Add(&listDirTool{})
	r.Add(&readFileTool{})
	r.Add(&writeFileTool{})
	r.Add(&loadSkillTool{})
	r.Add(&runSkillScriptTool{})
	r.Add(&delegateTool{})
	return r
}

// Add はツールを登録する。
func (r *Registry) Add(t Tool) {
	if _, ok := r.tools[t.Name()]; !ok {
		r.order = append(r.order, t.Name())
		sort.Strings(r.order)
	}
	r.tools[t.Name()] = t
}

// Get は名前でツールを引く。
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names は登録済みツール名を返す。
func (r *Registry) Names() []string {
	return append([]string{}, r.order...)
}

// Defs は許可されたツールだけをモデルへ渡す形に変換する。
func (r *Registry) Defs(allowed func(string) bool) []provider.ToolDef {
	var out []provider.ToolDef
	for _, n := range r.order {
		if !allowed(n) {
			continue
		}
		t := r.tools[n]
		out = append(out, provider.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return out
}

// argString は引数から文字列を取り出す。モデルは型を間違えることがあるため、
// 数値や真偽値で来ても文字列として受け取る。
func argString(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return "", fmt.Errorf("引数 %q が必要です", key)
	}
	switch s := v.(type) {
	case string:
		return s, nil
	default:
		return fmt.Sprintf("%v", s), nil
	}
}

func argStringOpt(args map[string]any, key string) string {
	s, err := argString(args, key)
	if err != nil {
		return ""
	}
	return s
}

// schema は JSON Schema を組み立てる小さな補助。
func schema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// truncate は出力が文脈を食い潰さないよう上限で切る。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n\n... (%d バイトを省略しました)", len(s)-max)
}
