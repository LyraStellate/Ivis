// Package engine は 1 ターンの実行ループを回す。
//
// スキル本文は毎ターンのプロンプトに載せない。載せればスキルが増えるほど
// 入力が膨らみ、ローカルモデルの限られたコンテキスト長をすぐ食い潰す。載せるのは
// 名前と説明の一覧だけで、本文はモデルが必要と判断した時点で読み込む。
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
	"github.com/LyraStellate/Ivis/internal/websearch"
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

// Question は利用者への問い 1 件。
//
// 承認と別の型にするのは、返るものが違うからである。承認は可否だが、これは
// 文が返る。同じ仕組みに載せると、どちらを待っているのか画面が判別できない。
type Question struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	// ToolCallID はどの呼び出しから出た問いかを指す。問いの表示を、
	// それを出した呼び出しの直下に置くために要る。
	ToolCallID string `json:"tool_call_id"`
	Text       string `json:"text"`
	// Choices は選ばせたい候補。空なら自由に書いてもらう。
	Choices []string `json:"choices,omitempty"`
}

// Asker は利用者へ問う窓口。HTTP 層が実装する。
//
// 承認と分けるのは、承認が「いま出す手を通すか」であるのに対し、これは
// 「何を作るか」を決める問いだからである。通す通さないの二択に押し込むと、
// 答えが返らない。
type Asker interface {
	// Ask は答えを返す。待機中も ctx の取り消しで中断できる。
	Ask(ctx context.Context, q Question) (string, error)
}

// イベント種別。UI はこれを見て表示を組み立てる。
const (
	EvtUserSaved    = "user_saved"
	EvtMessageStart = "message_start"
	EvtDelta        = "delta"
	EvtThinking     = "thinking"
	EvtMessageEnd   = "message_end"
	EvtToolCall     = "tool_call"
	// EvtToolOutput は実行中の道具が吐いた出力の断片。
	//
	// 推論の delta と同じ扱いで、会話には残らない — 残るのは EvtToolResult が
	// 運ぶ最終的な結果のほうである。控えが溢れたときは delta と一緒に
	// 真っ先に捨てられる (#470913)。
	EvtToolOutput    = "tool_output"
	EvtToolResult    = "tool_result"
	EvtApproval      = "approval_request"
	EvtQuestion      = "question"
	EvtDelegateStart = "delegate_start"
	EvtDelegateEnd   = "delegate_end"
	// チームセッションの手番 (#640275)。委譲と違って入れ子にならず横に
	// 並ぶので、深さ (Depth) は使わない。
	EvtTurnStart   = "turn_start"
	EvtTurnEnd     = "turn_end"
	EvtTeamMessage = "team_message"
	EvtError       = "error"
	// EvtNotice は失敗ではない知らせ。コンテキストを圧縮したことなど、
	// 会話の見え方が変わったことを伝える (#486237)。
	EvtNotice = "notice"
	// EvtStage はいま何を待っているか。始まりで名前を、終わりで空を流す。
	EvtStage = "stage"
	// EvtChanged は、画面が持っている一覧が古くなったこと。何が古くなったかを
	// Text に入れる。中身は載せない — 載せると同じものを 2 つの経路で
	// 組み立てることになり、食い違ったときにどちらが正か決められない (#189542)。
	EvtChanged = "changed"
	EvtUsage   = "usage"
	EvtDone    = "done"
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
	Question *Question        `json:"question,omitempty"`
	// PromptTokens はモデルへ送った入力のトークン数、ContextLimit はその
	// モデルのコンテキスト長。分母が分からないときは 0 で、画面は割合を出さない。
	PromptTokens int `json:"prompt_tokens,omitempty"`
	ContextLimit int `json:"context_limit,omitempty"`
	// Stage はいま何をしているか。空文字は「何もしていない」を表し、印を
	// 下ろすのに使う。何も届かない時間に、待っているものの名前を出すため
	// だけのもので、会話には残らない (#486237)。
	Stage string `json:"stage,omitempty"`
	// Done は Stage の進み具合。分母を持てるものが無いので、割合ではなく
	// できた量 (要約なら文字数) をそのまま出す。
	Done int `json:"done,omitempty"`

	// ここから下はチームセッションだけが使う (#640275)。
	//
	// To は宛先のメンバー。送り手は AgentID である。
	To string `json:"to,omitempty"`
	// Why は手番を取ることになった理由、Did はその手番でやったこと。
	// 本文 (Text) と分けて流すのは、画面が別々に描くためである。
	Why string `json:"why,omitempty"`
	Did string `json:"did,omitempty"`
	// Decision は依頼への返答の可否。
	Decision string `json:"decision,omitempty"`
	// Relation は送り手から宛先への関係 (報告 / 依頼 / 指示)。
	Relation string `json:"relation,omitempty"`
	// Queued は手番の行列に残っている数。何度も手番が入れ替わる間、画面には
	// 長く何も届かないので、あと何人待っているかが唯一の手がかりになる。
	Queued int `json:"queued,omitempty"`
	// Turn は何ターンめの手番か。利用者の発言を 1 とし、そこから何本たどった
	// かを数える。図の列と同じ数え方である (#512740)。
	Turn int `json:"turn,omitempty"`
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

// ErrNoAddressee は宛先が書かれていないこと。
//
// 誰に頼むかを決めるのは利用者である。名簿が 2 人以上あるとき、こちらで
// 選ぶ道は無い — 選べば、それは窓口を別の名前で復活させたことになる (#640275)。
var ErrNoAddressee = errors.New("宛先が書かれていません")

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
	case errors.Is(err, ErrNoAddressee):
		return "no_addressee"
	}
	return ""
}

// Emit はイベントを 1 件送る。
type Emit func(Event)

// Engine は実行ループ本体。
type Engine struct {
	Cfg    *config.Config
	Store  *store.Store
	Agents *agent.Set
	// TeamAgents はチームセッションでだけ使えるエージェント。共通と別の
	// 一覧にしてあるのは、Tier 0 の扱いと委譲先の一覧が違うためである
	// (#731906)。
	TeamAgents *agent.Set
	Skills     *skillreg.Registry
	Tools      *tools.Registry
	Provider   provider.Provider
	Approver   Approver
	// Asker は利用者へ問う窓口。無ければエージェントは問えず、自分で決める。
	Asker Asker
	// Search は Web 検索の取得元。設定に応じて差し替わる。
	Search websearch.Searcher
	// Procs は走らせたままのプロセス。ターンをまたいで生きるので、実行
	// ループではなくエンジンが持つ。
	Procs *tools.ProcSet

	// ctxLen はモデルごとのコンテキスト長。毎ターン提供元へ問い合わせるほど変わる
	// ものではない。ゼロ値で使えるので初期化は要らない。
	ctxLen sync.Map
}

// noteUsage は 1 ターンで使ったコンテキストの量を記録し、画面へ流す。
//
// 委譲された子では記録しない。見せているのは利用者が次に送れる量であり、
// 子は別のコンテキストで走るためである。
func (e *Engine) noteUsage(ctx context.Context, rc *runCtx, u *provider.Usage) {
	if u == nil || rc.depth != 0 {
		return
	}
	limit := e.contextLimit(ctx, rc.agent)
	if err := e.Store.SetContextUsage(ctx, rc.sessionID, u.PromptTokens, limit); err != nil {
		return
	}
	rc.emit(Event{Type: EvtUsage, PromptTokens: u.PromptTokens, ContextLimit: limit})
}

// contextLimit は割合の分母を返す。実際に使うコンテキスト長そのものである。
//
// モデルが持てる最大値ではなく、こちらが渡す値を分母にする。渡した値より
// 大きい分母で割ると、まだ余裕があるように見えているうちに溢れる。
func (e *Engine) contextLimit(ctx context.Context, a *agent.Agent) int {
	return e.numCtx(ctx, a)
}

// numCtx はそのエージェントの生成で使うコンテキスト長を返す。
//
// 明示しないと提供元の既定 (Ollama は 4096) が使われる。ツールの結果を
// 何度も往復する使い方では、それはすぐに埋まる。埋まると古い側から黙って
// 捨てられるため、指示文ごと失われ、応答は途中で終わる。何を渡しているかを
// こちらで決め切る。
func (e *Engine) numCtx(ctx context.Context, a *agent.Agent) int {
	// 定義に書かれていれば、それが利用者の意思である。
	if n := numOption(a.Options["num_ctx"]); n > 0 {
		return n
	}
	want := e.Cfg.ContextTokens
	if want <= 0 {
		want = config.Default().ContextTokens
	}
	// モデルが持てる以上を求めても意味がない。持てる値が分かるなら、
	// そちらで頭を打つ。
	if max := e.modelContext(ctx, a.Model); max > 0 && max < want {
		return max
	}
	return want
}

// modelContext はモデル自身が持つコンテキスト長を返す。分からなければ 0。
//
// 失敗も覚える。覚えないと、提供元が遅い相手のときに生成のたびに問い合わせ、
// そのたび待ち時間を丸ごと積み上げる。1 ターンでツールを 20 往復すれば、
// 待つだけで 100 秒になる。接続も無駄に増える。
//
// ただし覚える時間は短くする。落ちていた相手が戻ったときに、いつまでも
// 「分からない」を返し続けないため。
func (e *Engine) modelContext(ctx context.Context, model string) int {
	if v, ok := e.ctxLen.Load(model); ok {
		known := v.(ctxLenEntry)
		if known.n > 0 || time.Since(known.at) < ctxLenRetry {
			return known.n
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	n, err := e.Provider.ContextLength(ctx, model)
	if err != nil {
		e.ctxLen.Store(model, ctxLenEntry{n: 0, at: time.Now()})
		return 0
	}
	e.ctxLen.Store(model, ctxLenEntry{n: n, at: time.Now()})
	return n
}

// ctxLenEntry は問い合わせた結果と、いつ問い合わせたか。分からなかったことも
// 結果として覚えるので、時刻が要る。
type ctxLenEntry struct {
	n  int
	at time.Time
}

// ctxLenRetry は「分からなかった」を覚えておく長さ。
const ctxLenRetry = 2 * time.Minute

// numOption は設定から数値を取り出す。JSON から来ると float64 に、
// 画面や試験から来ると int になるため、どちらも受ける。
func numOption(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// systemPrompt はエージェントの指示文に、利用可能なスキルの一覧、任せられる
// エージェントの一覧、作業場所の説明を足したものを返す。
//
// エージェントの一覧は定義群から毎回組み直す。派生ファイルとして持たせると、
// 定義と一覧が食い違ったときにどちらが正か決められなくなる (#528664)。
func systemPrompt(a *agent.Agent, skills []*skillreg.Skill, delegates []*agent.Agent,
	workspace string, now time.Time, teamNote string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(a.Instructions))
	b.WriteString("\n\n")
	// 時刻を載せておかないと、時間に関わる問いのたびにコマンドを打つことに
	// なる。打ったところで、その出力が読めるとも限らない。
	fmt.Fprintf(&b, "現在は %s です。\n", clock(now))
	fmt.Fprintf(&b, "この会話の作業ディレクトリは %s です。\n", workspace)
	// 境界とシェルは指示文に書く。書かないとモデルは相対と絶対を取り違え、
	// 動かないコマンドを書く。
	if a.Unconfined {
		b.WriteString("パスは絶対指定もでき、この外のファイルも読み書きできます。\n")
		b.WriteString("相対指定はこのディレクトリが基準です。\n")
	} else {
		b.WriteString("ファイル操作はこの中に限られます。パスはここからの相対で指定します。\n")
	}
	fmt.Fprintf(&b, "コマンドは %s 越しに実行されます。\n", tools.ShellName())

	if len(skills) > 0 {
		b.WriteString("\n利用できるスキル (必要になったら load_skill で本文を読むこと):\n")
		for _, s := range skills {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, oneLine(s.Description, 200))
		}
	}
	b.WriteString("\n" + autonomy(a.Allows("ask_user")))

	// チームの名簿と進め方。会話の形態が違えば、進め方の説明も違う。
	b.WriteString(teamNote)

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

// autonomy は「どこまで自分で決めてよいか」を書く。
//
// 書かないと、モデルは一手ごとに確かめようとする。確かめる先が居ない場面でも
// 確かめようとするので、ターンは何も進まないまま終わる。止まってよい場面を
// 挙げ、それ以外は進めと明示するほうが、丁寧に書くよりよく効く。
//
// 問う手段の有無で文面を変えるのは、持っていない道具を勧めても、モデルは
// 代わりに本文で問いかけてしまうからである。本文の問いかけはターンの終わりで
// あって、誰も答えない。
func autonomy(canAsk bool) string {
	var b strings.Builder
	b.WriteString("\n進め方:\n")
	b.WriteString("- 依頼を最後までやり切ってから答えてください。途中経過の報告のために止まらないこと。\n")
	b.WriteString("- 調べれば分かることは調べ、決められることは決めて進めてください。\n")
	b.WriteString("- 選ぶ余地があるときは妥当な既定を選び、選んだ前提を答えに書き添えてください。\n")
	if canAsk {
		b.WriteString("- 手を止めてよいのは、答えによって作るものが変わり、かつ自分では決められないときだけです。\n")
		b.WriteString("  そのときは ask_user で問います。本文で問いかけても、それは答えの終わりとして扱われ、誰も答えません。\n")
	} else {
		b.WriteString("- 問い返す手段はありません。分からない点は前提を置いて進め、置いた前提を答えに書いてください。\n")
	}
	b.WriteString("- 失敗したら、理由を読んで別の手を試してください。同じ手を繰り返さないこと。\n")
	return b.String()
}

// clock は指示文に載せる時刻。曜日と時差まで書くのは、そこまで含めて
// はじめて「いつか」が一意に決まるためである。
func clock(t time.Time) string {
	week := [...]string{"日", "月", "火", "水", "木", "金", "土"}
	return t.Format("2006-01-02") + " (" + week[t.Weekday()] + ") " + t.Format("15:04 MST-07:00")
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
		case store.RoleSummary:
			// 要約は依頼ではなく前提なので、利用者の発言としては渡さない。
			// 何の文章かを書き添えないと、モデルはこれを新しい指示として読む。
			out = append(out, provider.Message{
				Role:    provider.RoleSystem,
				Content: summaryHeader + m.Content,
			})
		}
	}
	return out
}
