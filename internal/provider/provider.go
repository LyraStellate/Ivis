// Package provider はモデル提供元の抽象を定める。
//
// v1 の実装は Ollama のみである。この抽象を設けるのは将来の拡張性のためでは
// なく、実行ループのテストでモックを差し込めるようにするためである。
package provider

import (
	"context"
	"errors"
	"fmt"
)

// 会話の役割。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall はモデルが要求したツール呼び出し。
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Message は会話 1 発言。
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolName は Role が tool のとき、どのツールの結果かを示す。
	ToolName string `json:"tool_name,omitempty"`
	// Thinking はモデルが答えに至るまでの過程。本文と分けて持つ。混ぜると
	// 後から読み返したときに結論と過程の区別がつかなくなる。
	Thinking string `json:"thinking,omitempty"`
}

// ToolDef はモデルに渡すツールの定義。
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request は 1 回の生成要求。
type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolDef
	Options  map[string]any
	// Think はモデルの推論機能を使うか。対応しないモデルでは失敗する。
	Think bool
}

// EventType はストリーム上の出来事の種類。
type EventType string

const (
	// EventDelta はトークン片。
	EventDelta EventType = "delta"
	// EventThinking は推論の過程の断片。本文とは別に流す。
	EventThinking EventType = "thinking"
	// EventToolCalls はモデルがツール呼び出しを要求したこと。
	EventToolCalls EventType = "tool_calls"
	// EventDone は生成の正常終了。
	EventDone EventType = "done"
	// EventError は生成の失敗。部分出力はそれまでに流した delta が持つ。
	EventError EventType = "error"
)

// Usage は 1 回の生成で使われた量。提供元の実測値をそのまま持つ。
// 自前で数えないのは、数え方がモデルごとに違い、推定は必ずずれるためである。
type Usage struct {
	// PromptTokens はモデルへ送った入力のトークン数。
	PromptTokens int
	// EvalTokens は生成された出力のトークン数。
	EvalTokens int
}

// Event はストリーム上の 1 件。
type Event struct {
	Type      EventType
	Text      string
	ToolCalls []ToolCall
	Err       error
	// Usage は EventDone に載る。得られない提供元では nil。
	Usage *Usage
	// Truncated は EventDone に載る。文脈が尽きて生成が打ち切られたこと。
	// これを伝えないと、途中で終わった応答が答え終えたものと区別できない。
	Truncated bool
}

// Model は提供元が持つモデルの情報。
type Model struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Family     string `json:"family"`
	Parameters string `json:"parameters"`
}

// Provider はモデル提供元。
type Provider interface {
	// Name は表示用の名前。
	Name() string
	// Chat は生成を開始し、イベントのストリームを返す。ctx の取り消しで中断する。
	Chat(ctx context.Context, req Request) (<-chan Event, error)
	// Models は利用可能なモデルの一覧。
	Models(ctx context.Context) ([]Model, error)
	// ContextLength はモデルが持つ文脈長を返す。分からなければ 0 を返す。
	ContextLength(ctx context.Context, model string) (int, error)
	// Health は提供元に到達できるかを確かめる。
	Health(ctx context.Context) error
}

// ErrUnavailable は提供元に接続できないこと。利用者が次にすべきことは
// 「Ollama を起動する」であり、他の失敗と混ぜてはならない。
var ErrUnavailable = errors.New("モデル提供元に接続できません")

// ModelNotFoundError は指定モデルが提供元に存在しないこと。
// 利用者が次にすべきことは「モデルを pull する」である。
type ModelNotFoundError struct{ Model string }

func (e *ModelNotFoundError) Error() string {
	return fmt.Sprintf("モデル %q が見つかりません", e.Model)
}

// ToolsUnsupportedError はモデルがツール呼び出しに対応していないこと。
// この場合スキルの読み込みも委譲も機能しないため、その旨を利用者に示す。
type ToolsUnsupportedError struct{ Model string }

func (e *ToolsUnsupportedError) Error() string {
	return fmt.Sprintf("モデル %q はツール呼び出しに対応していません", e.Model)
}

// ThinkingUnsupportedError はモデルが推論に対応していないこと。
// 利用者が次にすべきことは「推論を切るか、対応するモデルへ変える」である。
type ThinkingUnsupportedError struct{ Model string }

func (e *ThinkingUnsupportedError) Error() string {
	return fmt.Sprintf("モデル %q は推論に対応していません", e.Model)
}
