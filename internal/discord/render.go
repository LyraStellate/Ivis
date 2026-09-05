package discord

import (
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/engine"
)

const (
	// detailMax は 1 行に載せる引数の長さ。
	detailMax = 200
	// bodyLimit は Discord のメッセージ本文の上限 (文字数)。
	bodyLimit = 2000
	// embedLimit は埋め込みの説明文の上限 (文字数)。
	embedLimit = 4096
	// failColor は失敗を示す色。話し手の色とは別に、これだけが赤い。
	failColor = 0xd06a6a
)

// 出来事の種別。起きた順に 1 つの並びへ積む。並びを分けて持つと、ツールと
// 推論と回答が実際にどの順で起きたかを後から復元できない。
const (
	kindThink    = "think"
	kindAnswer   = "answer"
	kindTool     = "tool"
	kindDelegate = "delegate"
)

// item は 1 つの出来事。Discord ではそれぞれが 1 通のメッセージになる。
type item struct {
	kind  string
	agent string
	depth int
	start time.Time
	end   time.Time

	// text は推論と回答の中身。
	text strings.Builder

	// parent は囲っている委譲の位置。-1 なら最上位。委譲された先の出来事は
	// その委譲の枠の中へ描くので、どこに属するかを持っておく必要がある。
	parent int

	// ツールと委譲。
	name    string
	detail  string
	callID  string
	failed  bool
	waiting bool
	// aborted は結果が返る前にターンが終わったこと。止めたのに「完了」と
	// 出ては、何が起きたか取り違える。
	aborted bool
	// approvalID は承認を待っている間だけ入る。押しボタンはその出来事の
	// メッセージに付く。何を承認するのかと、押す場所が離れていては選べない。
	approvalID string
	// questionID は問いへの答えを待っている間だけ入る。承認と分けるのは、
	// 答え方が違うからである。承認は押しボタン、問いは返信で受ける。
	questionID string
}

func (i *item) closed() bool { return !i.end.IsZero() }

// questionText は問いを 1 つの文にする。候補は添えるだけで、押しボタンには
// しない。答えは自由な文であり、候補どおりに答えるとは限らない。
func questionText(q *engine.Question) string {
	s := q.Text
	if len(q.Choices) > 0 {
		s += " (" + strings.Join(q.Choices, " / ") + ")"
	}
	return s
}

// turn は 1 ターンの経過。イベントで状態を更新し、いま出ているべき
// メッセージの並びを組み立てる。外部とはやり取りしない。
type turn struct {
	// nameOf は話し手の表示名を引く。出すのは ID ではなく名前で、定義に
	// 付けた名前がそのまま読める方がよい。
	nameOf func(agentID string) string

	items  []*item
	byCall map[string]int
	// openDelegate は走っている委譲の、深さごとの位置。
	openDelegate map[int]int

	err     string
	errKind string

	done    bool
	stopped bool
}

func newTurn(nameOf func(string) string) *turn {
	return &turn{nameOf: nameOf,
		byCall: map[string]int{}, openDelegate: map[int]int{}}
}

// open は末尾の出来事を返す。種別か話し手が変われば新しく開く。
func (t *turn) open(kind, agent string, depth int, now time.Time) *item {
	if n := len(t.items); n > 0 {
		last := t.items[n-1]
		if last.kind == kind && last.agent == agent && last.depth == depth && !last.closed() {
			return last
		}
	}
	t.closeText(now)
	it := &item{kind: kind, agent: agent, depth: depth, parent: t.container(depth), start: now}
	t.items = append(t.items, it)
	return it
}

// container は深さから、囲っている委譲の位置を返す。深さ d の出来事は、
// 深さ d-1 で始まった委譲の中で起きている。
func (t *turn) container(depth int) int {
	if depth <= 0 {
		return -1
	}
	if i, ok := t.openDelegate[depth-1]; ok {
		return i
	}
	return -1
}

// closeText は伸びている推論と回答を閉じる。別のことが起きた時点で、
// その塊は終わっている。
func (t *turn) closeText(now time.Time) {
	for _, it := range t.items {
		if (it.kind == kindThink || it.kind == kindAnswer) && !it.closed() {
			it.end = now
		}
	}
}

// apply はイベント 1 件を取り込む。
func (t *turn) apply(ev engine.Event, now time.Time) {
	switch ev.Type {
	case engine.EvtThinking:
		t.open(kindThink, ev.AgentID, ev.Depth, now).text.WriteString(ev.Text)

	case engine.EvtDelta:
		t.open(kindAnswer, ev.AgentID, ev.Depth, now).text.WriteString(ev.Text)

	case engine.EvtToolCall:
		t.closeText(now)
		t.byCall[ev.ToolCallID] = len(t.items)
		t.items = append(t.items, &item{kind: kindTool, agent: ev.AgentID, depth: ev.Depth,
			parent: t.container(ev.Depth), start: now, name: ev.Tool,
			detail: summarize(ev.Args), callID: ev.ToolCallID})

	case engine.EvtApproval:
		if i, ok := t.byCall[ev.ToolCallID]; ok && ev.Approval != nil {
			t.items[i].waiting = true
			t.items[i].approvalID = ev.Approval.ID
		}

	case engine.EvtQuestion:
		if i, ok := t.byCall[ev.ToolCallID]; ok && ev.Question != nil {
			t.items[i].waiting = true
			t.items[i].questionID = ev.Question.ID
			// 問いは全文が要る。切り詰めると、何を訊かれたのか分からない
			// まま答えることになる。
			t.items[i].detail = questionText(ev.Question)
		}

	case engine.EvtToolResult:
		i, ok := t.byCall[ev.ToolCallID]
		if !ok {
			return
		}
		it := t.items[i]
		it.end = now
		it.waiting = false
		it.approvalID = ""
		it.questionID = ""
		if strings.HasPrefix(ev.Result, "エラー: ") {
			it.failed = true
			it.detail = clip(oneLine(strings.TrimPrefix(ev.Result, "エラー: ")), detailMax)
		}

	case engine.EvtDelegateStart:
		t.closeText(now)
		t.items = append(t.items, &item{kind: kindDelegate, agent: ev.AgentID, depth: ev.Depth,
			parent: t.container(ev.Depth), start: now, name: ev.AgentID,
			detail: clip(oneLine(ev.Text), detailMax)})
		t.openDelegate[ev.Depth] = len(t.items) - 1

	case engine.EvtDelegateEnd:
		if i, ok := t.openDelegate[ev.Depth]; ok {
			// 中で走っていたものも、そこで終わっている。
			for _, it := range t.items {
				if it.parent == i && !it.closed() {
					it.end = now
				}
			}
			t.items[i].end = now
			if ev.Error != "" {
				t.items[i].failed = true
				t.items[i].detail = clip(oneLine(ev.Error), detailMax)
			}
			delete(t.openDelegate, ev.Depth)
		}

	case engine.EvtError:
		// 種類のない失敗は既に出ているものの繰り返しであることが多い。
		// 最初の 1 つを残す。
		if t.err == "" {
			t.err = oneLine(ev.Error)
			t.errKind = ev.Kind
		}
	}
}

// finish はターンの終わりを記録する。
func (t *turn) finish(now time.Time, stopped bool) {
	t.closeText(now)
	for _, it := range t.items {
		if !it.closed() {
			it.end = now
			it.aborted = true
			it.waiting = false
		}
	}
	t.done = true
	t.stopped = stopped
}
