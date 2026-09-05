package discord

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LyraStellate/Ivis/internal/engine"
)

func TestParseButton(t *testing.T) {
	kind, arg, ok := parseButton(approveID("x1", true))
	if !ok || kind != btnOK || arg != "x1" {
		t.Fatalf("承認を読めない: %q %q %v", kind, arg, ok)
	}
	if kind, _, _ := parseButton(approveID("x1", false)); kind != btnNo {
		t.Fatalf("拒否を %q として読んでいる", kind)
	}
	// 他のボットのボタンに反応してはいけない。
	if _, _, ok := parseButton("other:ok:x1"); ok {
		t.Fatal("自分のものでない識別子を受けている")
	}
	if _, _, ok := parseButton("ivis:ok:"); ok {
		t.Fatal("宛先の無い識別子を受けている")
	}
}

// arrange は承認待ちの状態を作り、その応答を待つ経路を返す。
func arrange(t *testing.T) (*harness, *run, chan bool) {
	t.Helper()
	h := newHarness(t)
	r := newRun(h.api, "c1", "m1", h.b.nameFor, time.Now)
	h.b.enter("s1", "u1", r)

	// 実行ループと同じ順で流す。ツールの呼び出しがあり、その承認を求める。
	r.emit(engine.Event{Type: engine.EvtToolCall, Tool: "run_command", ToolCallID: "a",
		AgentID: "general", Args: map[string]any{"command": "rm -rf ."}})
	r.emit(engine.Event{Type: engine.EvtApproval, ToolCallID: "a", Tool: "run_command",
		Approval: &engine.ApprovalRequest{ID: "a1"}})

	res := make(chan bool, 1)
	go func() {
		ok, _ := h.b.Request(context.Background(), engine.ApprovalRequest{
			ID: "a1", SessionID: "s1", Tool: "run_command",
			Arguments: map[string]any{"command": "rm -rf ."},
		})
		res <- ok
	}()

	// 登録されるまで待つ。押しても届かない状態で押しても確かめられない。
	for i := 0; i < 500; i++ {
		h.b.mu.Lock()
		_, ready := h.b.waits["a1"]
		h.b.mu.Unlock()
		if ready {
			return h, r, res
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("承認が登録されない")
	return nil, nil, nil
}

func TestApprovalShowsButtonsOnTheTool(t *testing.T) {
	_, r, _ := arrange(t)

	v := r.turn.view(time.Now())
	last := v[len(v)-1]
	if len(last.p.Buttons) != 2 {
		t.Fatalf("押しボタンが出ていない: %#v", last)
	}
	if !strings.Contains(body(v), "rm -rf") {
		t.Fatalf("何を承認するのかが同じ場所に無い:%s", body(v))
	}
}

func TestApprovalOnlyAskerCanPress(t *testing.T) {
	h, r, res := arrange(t)

	// 呼びかけていない人が押しても通らない。通ると、依頼していない人が
	// 実行を決められることになる。
	accepted, known := h.b.resolve("a1", true, "u2")
	if accepted || !known {
		t.Fatalf("他人の操作が accepted=%v known=%v になった", accepted, known)
	}
	select {
	case <-res:
		t.Fatal("他人が押して承認が返った")
	case <-time.After(20 * time.Millisecond):
	}

	if accepted, _ := h.b.resolve("a1", true, "u1"); !accepted {
		t.Fatal("本人が押せない")
	}
	select {
	case ok := <-res:
		if !ok {
			t.Fatal("承認が拒否として返った")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("承認が返らない")
	}

	// 押した後にボタンが残っていると、もう一度押せるように見える。
	for _, m := range r.turn.view(time.Now()) {
		if len(m.p.Buttons) != 0 {
			t.Fatalf("押した後もボタンが残っている: %#v", m.p.Buttons)
		}
	}
}

func TestApprovalTimesOutAsRefusal(t *testing.T) {
	old := approveWait
	approveWait = 30 * time.Millisecond
	defer func() { approveWait = old }()

	_, _, res := arrange(t)
	select {
	case ok := <-res:
		if ok {
			t.Fatal("押されていないのに承認になった")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("期限が来ても返らない")
	}
}

func TestApprovalUnknownIsReported(t *testing.T) {
	h := newHarness(t)
	if _, known := h.b.resolve("なにか", true, "u1"); known {
		t.Fatal("知らない承認を受け付けている")
	}
}

func TestStopCancelsTheRunInThatChannel(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// 走っている状態を作る。コマンドはチャンネルからしか会話を知れない。
	h.b.serve(ctx, h.api, mention("なにか"))
	sess, err := h.st.SessionByChannel(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	stopped := false
	h.runs.Begin(sess.ID, func() { stopped = true })
	defer h.runs.End(sess.ID)
	h.b.enter(sess.ID, "u1", newRun(h.api, "c1", "m", h.b.nameFor, time.Now))
	defer h.b.leave(sess.ID)

	// 止めるのは誰にとっても安全なので、使える相手を絞らない。承認と違い、
	// その場に居合わせた人が止められないと緊急停止の役に立たない。
	if !h.b.stop(ctx, "c1") {
		t.Fatal("止められない")
	}
	if !stopped {
		t.Fatal("中断が呼ばれていない")
	}
	if h.b.stop(ctx, "そんなチャンネル") {
		t.Fatal("知らないチャンネルを止めたことになっている")
	}
}

func TestOwnsOnlyWhileRunning(t *testing.T) {
	h := newHarness(t)
	if h.b.Owns("s1") {
		t.Fatal("動かしていない会話を自分のものと言っている")
	}
	h.b.enter("s1", "u1", newRun(h.api, "c", "m", h.b.nameFor, time.Now))
	if !h.b.Owns("s1") {
		t.Fatal("動かしている会話を自分のものと言えていない")
	}
	h.b.leave("s1")
	if h.b.Owns("s1") {
		t.Fatal("終わった会話を握ったままになっている")
	}
}
