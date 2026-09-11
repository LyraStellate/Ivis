// Package tools はエージェントが呼べるツールを定める。
//
// ここは engine を import しない。委譲のようにエンジン側の機能を必要とする
// ツールは、ExecContext 経由で関数を受け取る。
package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/websearch"
)

// ExecContext はツール 1 回の実行に必要な一式。
type ExecContext struct {
	// Workspace は相対指定の基準。境界を課す場合は、越えてはならない境界でもある。
	Workspace string
	// Confined が false のとき、境界を課さない。絶対指定も受け付け、
	// コマンドも会話の外で走らせられる。何ができるかは定義に書かれている。
	Confined bool
	// Skills はスキルの参照先。
	Skills *skillreg.Registry
	// ScriptIdle と CommandIdle は、動かないまま待つ上限。
	//
	// 実行そのものには時間の上限を置かない。時間で切ると、正しく進んでいる
	// 長い仕事まで止まる — npm install も go build も当たり前に数分かかり、
	// 途中で切れば中途半端に入ったライブラリが残る。止めるべきなのは進んで
	// いないときだけである (#470913)。
	//
	// 「動いている」は、出力が届いたことと、木全体の仕事量 (CPU 時間・I/O)
	// が進んだことの両方で数え直す。用途が違えば妥当な長さも違うので、
	// スクリプトとコマンドで別に持つ。
	ScriptIdle  time.Duration
	CommandIdle time.Duration
	// Search は検索の取得元。設定されていなければ検索は使えない。
	Search websearch.Searcher
	// AgentID は呼び出し元のエージェント。
	AgentID string
	// Session は会話の識別子。走らせたままのプロセスを会話ごとに束ねる。
	Session string
	// Procs は走らせたままのプロセス。ツール呼び出しをまたいで生き続けるので、
	// 実行 1 回で閉じるものではなく、それより長く生きるものを受け取る。
	Procs *ProcSet
	// Ask は利用者へ問い、答えが返るまで待つ。engine が注入する。問える
	// 相手が居ない場面では nil になる。
	Ask func(ctx context.Context, question string, choices []string) (string, error)
	// Delegate は委譲の実行。engine が注入する。
	Delegate func(ctx context.Context, agentID, task string) (string, error)
	// CheckDelegate は委譲先として許可されているかの判定。許されないときは
	// 理由を返す。理由はそのままモデルへ渡り、次の手を考える材料になる。
	CheckDelegate func(agentID string) error
	// Team はチームセッションの手番 1 回分。直列の会話では nil で、チーム
	// 専用のツールはそもそもモデルへ渡らない (#640275)。
	Team *TeamContext
	// Tickets はチケットの読み書き先。チーム専用のツールだけが使う (#189542)。
	Tickets *store.Store
	// Notice は利用者への知らせを 1 件出す。取り消せない操作を、道具の
	// 結果としてモデルへ返すだけで済ませないために要る。モデルにしか
	// 見えない場所に書いても、消えたことは誰にも伝わらない。
	Notice func(text string)
	// Output は実行中の出力を画面へ流す。終わるまで何も見えないと、長く
	// 走るものは止まっているのと区別が付かない (#470913)。
	//
	// 流すのは画面のためだけで、モデルへ渡す結果は Execute の戻り値が全部
	// 持つ。nil のこともある (画面の無い経路)。
	Output func(chunk string)
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

// NewRegistry は標準のツールを備えたレジストリを返す。
func NewRegistry() *Registry {
	r := &Registry{tools: map[string]Tool{}}
	r.Add(&listDirTool{})
	r.Add(&readFileTool{})
	r.Add(&writeFileTool{})
	r.Add(&loadSkillTool{})
	r.Add(&runSkillScriptTool{})
	r.Add(&delegateTool{})
	r.Add(&findFilesTool{})
	r.Add(&searchTextTool{})
	r.Add(&editFileTool{})
	r.Add(&moveFileTool{})
	r.Add(&deleteFileTool{})
	r.Add(&runCommandTool{})
	r.Add(&webSearchTool{})
	r.Add(&fetchURLTool{})
	r.Add(&startProcessTool{})
	r.Add(&readProcessTool{})
	r.Add(&writeProcessTool{})
	r.Add(&stopProcessTool{})
	r.Add(&askUserTool{})
	// ここから下はチームセッションでしか渡らない。直列の会話では意味を
	// 持たないので、セッションの種類でふるう (#640275)。
	r.Add(&sendMessageTool{})
	r.Add(&createTicketTool{})
	r.Add(&updateTicketTool{})
	r.Add(&getTicketTool{})
	r.Add(&listTicketsTool{})
	r.Add(&deleteTicketTool{})
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
//
// team が偽のときチーム専用のツールを外す。定義側で "*" を選んでいても、
// 直列の会話にチームのツールを渡さない。渡すと、モデルは呼べるものとして
// 扱い、呼んでから「この会話では使えません」と返されることになる。
func (r *Registry) Defs(allowed func(string) bool, team bool) []provider.ToolDef {
	var out []provider.ToolDef
	for _, n := range r.order {
		if !allowed(n) {
			continue
		}
		t := r.tools[n]
		if !team && IsTeamOnly(t) {
			continue
		}
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

// argInt は引数から整数を取り出す。JSON からは float64 で来る。
func argInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// oneLine は改行を畳んで長さを切る。一覧に載せる要約に使う。
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "..."
	}
	return s
}

// argBool は引数から真偽値を取り出す。モデルは "true" のように文字列で
// 返すことがあるため、そちらも受ける。
func argBool(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "True" || v == "1"
	}
	return false
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

// truncate は出力がコンテキストを食い潰さないよう上限で切る。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n\n... (%d バイトを省略しました)", len(s)-max)
}
