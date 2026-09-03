// Package engine は 1 ターンの実行ループを回す。
//
// スキル本文は毎ターンのプロンプトに載せない。載せればスキルが増えるほど
// 入力が膨らみ、ローカルモデルの限られた文脈長をすぐ食い潰す。載せるのは
// 名前と説明の一覧だけで、本文はモデルが必要と判断した時点で読み込む。
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
)

// ApprovalRequest は 1 件の承認要求。
type ApprovalRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	// ToolCallID はどのツール呼び出しに対する承認かを指す。承認の表示を
	// 対応する呼び出しの直下に出すために要る。ツール名だけでは、同じツールが
	// 同一ターンで 2 回呼ばれたときにどちらか特定できない。
	ToolCallID string         `json:"tool_call_id"`
	AgentID    string         `json:"agent_id"`
	Tool       string         `json:"tool"`
	Arguments  map[string]any `json:"arguments"`
}

// Approver は利用者へ承認を求める窓口。HTTP 層が実装する。
type Approver interface {
	// Request は承認の可否を返す。待機中も ctx の取り消しで中断できる。
	Request(ctx context.Context, req ApprovalRequest) (bool, error)
}

// イベント種別。UI はこれを見て表示を組み立てる。
const (
	EvtMessageStart  = "message_start"
	EvtDelta         = "delta"
	EvtThinking      = "thinking"
	EvtMessageEnd    = "message_end"
	EvtToolCall      = "tool_call"
	EvtToolResult    = "tool_result"
	EvtApproval      = "approval_request"
	EvtDelegateStart = "delegate_start"
	EvtDelegateEnd   = "delegate_end"
	EvtError         = "error"
	EvtDone          = "done"
)

// Event はストリームで UI へ送る 1 件。
type Event struct {
	Type      string `json:"type"`
	MessageID string `json:"message_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	Depth     int    `json:"depth"`
	Text      string `json:"text,omitempty"`
	Tool      string `json:"tool,omitempty"`
	// ToolCallID は tool_call と tool_result と approval_request を結び付ける。
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	// Kind は失敗の種類。画面が「Ollama を起動する」「モデルを pull する」と
	// いった次の行動を出し分けるために要る。種類が無いと、上端の帯と通知が
	// 同じことを二重に言う。
	Kind     string           `json:"kind,omitempty"`
	Approval *ApprovalRequest `json:"approval,omitempty"`
}

// reportedError は、その失敗が既にイベントとして流されたことを示す包み。
// 実行ループは失敗をイベントで知らせたうえで同じ error を返すため、受け手が
// それをもう一度流すと、同じ文が 2 度並ぶ。
type reportedError struct{ error }

func (e reportedError) Unwrap() error { return e.error }

// reported は「イベントとして流し済み」の印を付ける。
func reported(err error) error { return reportedError{err} }

// Reported は失敗が既にイベントとして流されたかを返す。
func Reported(err error) bool {
	var r reportedError
	return errors.As(err, &r)
}

// KindOf は失敗を種類の名前へ写す。まとめて「エラーが発生しました」にすると、
// 提供元の起動忘れなのか設定の誤りなのかを利用者が判断できない。
func KindOf(err error) string {
	var notFound *provider.ModelNotFoundError
	var noTools *provider.ToolsUnsupportedError
	var noThink *provider.ThinkingUnsupportedError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, provider.ErrUnavailable):
		return "provider_unavailable"
	case errors.As(err, &notFound):
		return "model_not_found"
	case errors.As(err, &noTools):
		return "tools_unsupported"
	case errors.As(err, &noThink):
		return "thinking_unsupported"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	}
	return ""
}

// Emit はイベントを 1 件送る。
type Emit func(Event)

// Engine は実行ループ本体。
type Engine struct {
	Cfg      *config.Config
	Store    *store.Store
	Agents   *agent.Set
	Skills   *skillreg.Registry
	Tools    *tools.Registry
	Provider provider.Provider
	Approver Approver
}

// systemPrompt はエージェントの指示文に、利用可能なスキルの一覧、任せられる
// エージェントの一覧、作業場所の説明を足したものを返す。
//
// エージェントの一覧は定義群から毎回組み直す。派生ファイルとして持たせると、
// 定義と一覧が食い違ったときにどちらが正か決められなくなる (#528664)。
func systemPrompt(a *agent.Agent, skills []*skillreg.Skill, delegates []*agent.Agent, workspace string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(a.Instructions))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "作業ディレクトリは %s です。ファイル操作はこの中に限られます。\n", workspace)

	if len(skills) > 0 {
		b.WriteString("\n利用できるスキル (必要になったら load_skill で本文を読むこと):\n")
		for _, s := range skills {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, oneLine(s.Description, 200))
		}
	}
	if len(delegates) > 0 {
		fmt.Fprintf(&b, "\nあなたは Tier %d です。仕事を任せられるのは自分より下位 "+
			"(Tier の数字が大きい) の相手だけで、次のエージェントを delegate で呼べます:\n", a.Tier)
		for _, d := range delegates {
			fmt.Fprintf(&b, "- %s (Tier %d, %s): %s\n", d.ID, d.Tier, d.Name,
				oneLine(describe(d), 200))
		}
	}
	return b.String()
}

// describe は一覧に載せる説明。空のままにすると、モデルは名前だけで
// 呼び分けることになり、選択が当てずっぽうになる。
func describe(a *agent.Agent) string {
	if strings.TrimSpace(a.Description) == "" {
		return "(説明が書かれていません)"
	}
	return a.Description
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		// 多バイト文字の途中で切らないよう rune 単位で扱う。
		r := []rune(s)
		if len(r) > max {
			return string(r[:max]) + "..."
		}
	}
	return s
}

// toProviderMessages は保存済みの履歴をモデルへの入力へ変換する。
func toProviderMessages(history []*store.Message) []provider.Message {
	out := make([]provider.Message, 0, len(history))
	for _, m := range history {
		switch m.Role {
		case provider.RoleUser, provider.RoleAssistant, provider.RoleTool:
			out = append(out, provider.Message{
				Role:      m.Role,
				Content:   m.Content,
				ToolCalls: m.ToolCalls,
				ToolName:  m.ToolName,
			})
		}
	}
	return out
}
