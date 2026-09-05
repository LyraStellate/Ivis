package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/LyraStellate/Ivis/internal/engine"
)

func at(sec int) time.Time { return time.Unix(1000, 0).Add(time.Duration(sec) * time.Second) }

// 表示名は定義から引く。委譲に出るのは ID ではなく名前になる。
func testName(agentID string) string {
	if agentID == "researcher" {
		return "Researcher"
	}
	return "総合"
}

func feed(evs ...engine.Event) *turn {
	tn := newTurn(testName)
	for i, ev := range evs {
		tn.apply(ev, at(i))
	}
	return tn
}

// body は 1 通にまとまった中身を返す。分かれている場合は繋げて見る。
func body(ms []msg) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, m.p.Content)
	}
	return strings.Join(parts, "\n")
}

func TestEverythingLivesInOneMessage(t *testing.T) {
	tn := feed(
		engine.Event{Type: engine.EvtThinking, Text: "どこを見るか", AgentID: "general"},
		engine.Event{Type: engine.EvtToolCall, Tool: "search_text", ToolCallID: "a", AgentID: "general",
			Args: map[string]any{"pattern": "func Rewind"}},
		engine.Event{Type: engine.EvtToolResult, ToolCallID: "a", Result: "3 件"},
		engine.Event{Type: engine.EvtDelta, Text: "分かりました", AgentID: "general"},
	)
	tn.finish(at(20), false)

	v := tn.view(at(20))
	if len(v) != 1 {
		t.Fatalf("%d 通に分かれている: %v", len(v), keys(v))
	}
	if !v[0].reply {
		t.Fatal("呼びかけへ紐付いていない")
	}
	// 起きた順にそのまま並ぶ。
	got := v[0].p.Content
	order := []string{"推論: ", "`search_text : complete`", "分かりました"}
	last := -1
	for _, want := range order {
		i := strings.Index(got, want)
		if i < 0 {
			t.Fatalf("%q が無い:\n%s", want, got)
		}
		if i < last {
			t.Fatalf("並びが時間順でない (%q):\n%s", want, got)
		}
		last = i
	}
}

func TestAnswerIsThePlainText(t *testing.T) {
	tn := feed(
		engine.Event{Type: engine.EvtToolCall, Tool: "read_file", ToolCallID: "a", AgentID: "general",
			Args: map[string]any{"path": "go.mod"}},
		engine.Event{Type: engine.EvtToolResult, ToolCallID: "a", Result: "ok"},
		engine.Event{Type: engine.EvtDelta, Text: "go.mod を読みました", AgentID: "general"},
	)
	tn.finish(at(9), false)

	lines := strings.Split(tn.view(at(9))[0].p.Content, "\n")
	answer := lines[len(lines)-1]
	// 回答だけが飾りの無い地の文になる。ここで見分けが付かないと、経過と
	// 答えのどちらを読めばよいか分からない。
	if answer != "go.mod を読みました" {
		t.Fatalf("回答に飾りが付いている: %q", answer)
	}
	// 経過との間は 1 行空ける。
	if lines[len(lines)-2] != "" {
		t.Fatalf("経過と回答が続いている:\n%s", strings.Join(lines, "\n"))
	}
}

func TestThinkingIsAFencedBlock(t *testing.T) {
	tn := feed(engine.Event{Type: engine.EvtThinking,
		Text: "手前を見る\n```\nうしろも見る", AgentID: "general"})

	got := tn.view(at(1))[0].p.Content
	if !strings.HasPrefix(got, "```") || !strings.HasSuffix(got, "```") {
		t.Fatalf("推論が塊として区切られていない: %q", got)
	}
	// 中に区切りと同じ記号が混ざると、そこで切れる。
	if strings.Count(got, "```") != 2 {
		t.Fatalf("区切りが途中で切れている: %q", got)
	}
	// 途中の改行はそのまま残る。1 行ずつ囲うのではない。
	if !strings.Contains(got, "手前を見る\n") {
		t.Fatalf("行の形が崩れている: %q", got)
	}
}

func TestThinkingGrowsThenCollapses(t *testing.T) {
	tn := feed(
		engine.Event{Type: engine.EvtThinking, Text: "まず仕様を確かめる。", AgentID: "general"},
		engine.Event{Type: engine.EvtThinking, Text: "次に実装を見る。", AgentID: "general"},
	)

	live := tn.view(at(3))[0].p.Content
	// 省略しない。切り詰めると、何を考えていたかを追えなくなる。
	for _, want := range []string{"まず仕様を確かめる。", "次に実装を見る。"} {
		if !strings.Contains(live, want) {
			t.Fatalf("推論が欠けている (%q):\n%s", want, live)
		}
	}

	// 本文が出たら推論は終わり。長さだけへ縮める。
	tn.apply(engine.Event{Type: engine.EvtDelta, Text: "答えです", AgentID: "general"}, at(12))
	done := tn.view(at(12))[0].p.Content
	if !strings.Contains(done, "```\n推論: 12 秒\n```") {
		t.Fatalf("推論が縮んでいない:\n%s", done)
	}
	if strings.Contains(done, "まず仕様") {
		t.Fatalf("縮めた後も中身が残っている:\n%s", done)
	}
}

func TestLongThinkingSpreadsThenShrinks(t *testing.T) {
	long := strings.Repeat("ながい推論の行です。\n", 300)
	tn := feed(engine.Event{Type: engine.EvtThinking, Text: long, AgentID: "general"})

	spread := tn.view(at(1))
	if len(spread) < 2 {
		t.Fatalf("長い推論が %d 通にしか分かれていない", len(spread))
	}
	// 分かれても、それぞれが閉じた塊になっている。開いたまま切ると、
	// 続きのメッセージ全体がコードとして表示される。
	for i, m := range spread {
		if strings.Count(m.p.Content, "```")%2 != 0 {
			t.Fatalf("%d 通目が閉じていない:\n%s", i, m.p.Content)
		}
	}

	tn.apply(engine.Event{Type: engine.EvtDelta, Text: "答え", AgentID: "general"}, at(30))
	if n := len(tn.view(at(30))); n != 1 {
		t.Fatalf("縮めた後も %d 通ある", n)
	}
}

func TestToolShowsItsStage(t *testing.T) {
	tn := feed(engine.Event{Type: engine.EvtToolCall, Tool: "run_command", ToolCallID: "a",
		AgentID: "general", Args: map[string]any{"command": "go test"}})

	if got := tn.view(at(1))[0].p.Content; got != "`run_command : running`" {
		t.Fatalf("走っていることが分からない: %q", got)
	}

	tn.apply(engine.Event{Type: engine.EvtToolResult, ToolCallID: "a", Result: "ok"}, at(3))
	if got := tn.view(at(3))[0].p.Content; got != "`run_command : complete`" {
		t.Fatalf("終わったことが分からない: %q", got)
	}
}

func TestFailedToolSaysWhy(t *testing.T) {
	tn := feed(
		engine.Event{Type: engine.EvtToolCall, Tool: "run_command", ToolCallID: "a", AgentID: "general",
			Args: map[string]any{"command": "go test"}},
		engine.Event{Type: engine.EvtToolResult, ToolCallID: "a", Result: "エラー: 終了コード 1"},
	)
	got := tn.view(at(5))[0].p.Content
	if !strings.Contains(got, "`run_command : failed`") || !strings.Contains(got, "終了コード 1") {
		t.Fatalf("何が起きたかが出ていない: %q", got)
	}
}

func TestAbortedToolIsNotCalledDone(t *testing.T) {
	tn := feed(engine.Event{Type: engine.EvtToolCall, Tool: "run_command", ToolCallID: "a",
		AgentID: "general", Args: map[string]any{"command": "sleep 100"}})
	tn.finish(at(9), true)

	// 止めたのに complete と出ては、何が起きたかを取り違える。
	got := tn.view(at(9))[0].p.Content
	if strings.Contains(got, stageComplete) || !strings.Contains(got, stageAborted) {
		t.Fatalf("止めたことが伝わらない: %q", got)
	}
}

func TestDelegateIsSetInOneStep(t *testing.T) {
	tn := feed(
		engine.Event{Type: engine.EvtDelegateStart, AgentID: "researcher", Text: "仕様を調べて"},
		engine.Event{Type: engine.EvtThinking, Text: "どこに書いてあるか", Depth: 1, AgentID: "researcher"},
	)

	// 任された先の出来事は一段内へ寄る。どこからどこまでが誰の作業かは
	// これで読む。
	growing := tn.view(at(2))[0].p.Content
	if !strings.Contains(growing, "> **Researcher へ委譲**") {
		t.Fatalf("誰に任せたかが出ていない:\n%s", growing)
	}
	for _, line := range strings.Split(growing, "\n") {
		if line != "" && !strings.HasPrefix(line, "> ") {
			t.Fatalf("委譲の中で外れている行がある: %q", line)
		}
	}
	if !strings.Contains(growing, "どこに書いてあるか") {
		t.Fatalf("中の推論が出ていない:\n%s", growing)
	}

	tn.apply(engine.Event{Type: engine.EvtToolCall, Tool: "fetch_url", ToolCallID: "b", Depth: 1,
		AgentID: "researcher", Args: map[string]any{"url": "https://go.dev"}}, at(5))
	if got := tn.view(at(5))[0].p.Content; !strings.Contains(got, "> `fetch_url : running`") {
		t.Fatalf("中のツールが出ていない:\n%s", got)
	}

	tn.apply(engine.Event{Type: engine.EvtToolResult, ToolCallID: "b", Result: "ok"}, at(8))
	tn.apply(engine.Event{Type: engine.EvtDelegateEnd, AgentID: "researcher"}, at(9))

	// 終わったら 1 行へ単純化する。
	done := tn.view(at(9))[0].p.Content
	if done != "> **Researcher へ委譲**" {
		t.Fatalf("1 行になっていない:\n%s", done)
	}
}

func TestDelegateSplitClosesItsBlock(t *testing.T) {
	long := strings.Repeat("ながい推論の行です。\n", 300)
	tn := feed(
		engine.Event{Type: engine.EvtDelegateStart, AgentID: "researcher", Text: "調べて"},
		engine.Event{Type: engine.EvtThinking, Text: long, Depth: 1, AgentID: "researcher"},
	)

	// 引用の中の区切りを見落とすと、途中で切ったときに開いたままになる。
	for i, m := range tn.view(at(2)) {
		if strings.Count(m.p.Content, "```")%2 != 0 {
			t.Fatalf("%d 通目が閉じていない:\n%s", i, tail(m.p.Content, 120))
		}
	}
}

func TestApprovalButtonsRideTheLastMessage(t *testing.T) {
	tn := feed(
		engine.Event{Type: engine.EvtToolCall, Tool: "run_command", ToolCallID: "a", AgentID: "general",
			Args: map[string]any{"command": "rm -rf ."}},
		engine.Event{Type: engine.EvtApproval, ToolCallID: "a", Tool: "run_command",
			Approval: &engine.ApprovalRequest{ID: "p1"}},
	)

	v := tn.view(at(3))
	last := v[len(v)-1]
	if len(last.p.Buttons) != 2 {
		t.Fatalf("承認の押しボタンが無い: %#v", last)
	}
	// 何を承認するのかと、押す場所が離れていては選べない。
	if !strings.Contains(body(v), "rm -rf") {
		t.Fatalf("何を実行するかが同じ場所に無い:\n%s", body(v))
	}
	if !strings.Contains(body(v), "`run_command : approval`") {
		t.Fatalf("承認待ちだと分からない:\n%s", body(v))
	}
}

func TestFailureAdvises(t *testing.T) {
	tn := feed(engine.Event{Type: engine.EvtError,
		Error: "ollama に接続できません", Kind: "provider_unavailable"})
	tn.finish(at(1), false)

	got := tn.view(at(1))[0].p.Content
	if !strings.Contains(got, "**失敗:**") {
		t.Fatalf("失敗が目立たない: %q", got)
	}
	if !strings.Contains(got, "起動しているか") {
		t.Fatalf("次に何をすればよいかが無い:\n%s", got)
	}
}

func TestStoppedIsShown(t *testing.T) {
	tn := feed(engine.Event{Type: engine.EvtDelta, Text: "途中まで", AgentID: "general"})
	tn.finish(at(3), true)

	if !strings.Contains(tn.view(at(3))[0].p.Content, "停止しました") {
		t.Fatalf("止めたことが伝わらない:\n%s", tn.view(at(3))[0].p.Content)
	}
}

func TestSilentTurnSaysSo(t *testing.T) {
	tn := feed()
	if tn.view(at(0)) != nil {
		t.Fatal("何も起きていないのに投稿している")
	}

	tn.finish(at(1), false)
	// 黙って終わると、呼びかけが届いていないのか、届いて何も返らなかった
	// のかが区別できない。
	v := tn.view(at(1))
	if len(v) != 1 || !strings.Contains(v[0].p.Content, "応答がありませんでした") {
		t.Fatalf("何も伝えていない: %#v", v)
	}
}

func TestAnswerMarksStreaming(t *testing.T) {
	tn := feed(engine.Event{Type: engine.EvtDelta, Text: "途中", AgentID: "general"})
	if !strings.HasSuffix(tn.view(at(1))[0].p.Content, "▍") {
		t.Fatal("生成中の印が無い")
	}
	tn.finish(at(2), false)
	if strings.Contains(tn.view(at(2))[0].p.Content, "▍") {
		t.Fatal("終わった後も生成中の印が残っている")
	}
}

func TestNoStopButton(t *testing.T) {
	tn := feed(engine.Event{Type: engine.EvtDelta, Text: "考え中", AgentID: "general"})

	// 打ち切りはコマンドで行う。押しボタンは承認のときだけ出る。
	for _, m := range tn.view(at(1)) {
		if len(m.p.Buttons) != 0 {
			t.Fatalf("押しボタンが付いている: %#v", m.p.Buttons)
		}
	}
}

func keys(ms []msg) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.key)
	}
	return out
}
