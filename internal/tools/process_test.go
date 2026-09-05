package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// askScript は「1 行読んで、それを言い返す」だけの手続きを置き、その走らせ方を
// 返す。対話を求めるものの最小形である。
//
// 1 行のコマンドではなくファイルにするのは、cmd の遅延展開を避けるためである。
// 1 行に書くと読んだ値が使えず、試験が確かめたいこと (読めたか) とは別の
// ところで転ぶ。
func askScript(t *testing.T, dir string) string {
	t.Helper()
	name, body := "ask.sh", "printf 'name? '\nread L\necho \"got $L\"\n"
	if runtime.GOOS == "windows" {
		name, body = "ask.bat", "@echo off\nset /p L=name? \necho got %L%\n"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return `"` + path + `"`
	}
	return "sh " + path
}

func procCtx(t *testing.T) *ExecContext {
	t.Helper()
	// t.TempDir は後片付けで消せないと失敗する。プロセスがその場所を掴んだ
	// まま残る場合があるので、消さない場所を使う。
	dir, err := os.MkdirTemp(os.TempDir(), "ivis-proc")
	if err != nil {
		t.Fatal(err)
	}
	ps := NewProcSet()
	t.Cleanup(ps.Close)
	return &ExecContext{Workspace: dir, Confined: true, Session: "s1", Procs: ps}
}

// 対話を求めるコマンドは、起動と入力と読み取りが別の呼び出しにならないと
// 扱えない。run_command では、問いを読む前に答えを決めることになる。
func TestProcessAnswersAPrompt(t *testing.T) {
	ec := procCtx(t)
	ctx := context.Background()

	out, err := (&startProcessTool{}).Execute(ctx, ec, map[string]any{
		"command": askScript(t, ec.Workspace),
		"name":    "ask",
		"wait_ms": 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "name?") {
		t.Fatalf("問いが読めていない: %q", out)
	}

	out, err = (&writeProcessTool{}).Execute(ctx, ec, map[string]any{
		"name":    "ask",
		"input":   "ivis",
		"wait_ms": 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "got ivis") {
		t.Fatalf("答えが届いていない: %q", out)
	}
}

// 読み取りは、前回より後に出た分を待つ。以前の出力を根拠に「もう途切れて
// いる」と判断すると、これから出る応答を待たずに空で戻る。
func TestReadWaitsForNewOutput(t *testing.T) {
	ec := procCtx(t)
	ctx := context.Background()

	line := "echo first & " + sleepCmd(1) + " & echo second"
	if runtime.GOOS != "windows" {
		line = "echo first; sleep 1; echo second"
	}
	if _, err := (&startProcessTool{}).Execute(ctx, ec, map[string]any{
		"command": line, "name": "two", "wait_ms": 500,
	}); err != nil {
		t.Fatal(err)
	}

	out, err := (&readProcessTool{}).Execute(ctx, ec, map[string]any{
		"name": "two", "wait_ms": 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "second") {
		t.Fatalf("後から出た分が読めていない: %q", out)
	}
	if strings.Contains(out, "first") {
		t.Errorf("読み取り済みの分が再び返っている: %q", out)
	}
}

// 止めたら止まっていなければならない。終わったかどうかは、次に何をするかの
// 判断材料になる。
func TestStopProcess(t *testing.T) {
	ec := procCtx(t)
	ctx := context.Background()

	if _, err := (&startProcessTool{}).Execute(ctx, ec, map[string]any{
		"command": sleepCmd(30), "name": "long", "wait_ms": 300,
	}); err != nil {
		t.Fatal(err)
	}
	p, ok := ec.Procs.Get("s1", "long")
	if !ok || !p.Alive() {
		t.Fatal("起動できていない")
	}
	if _, err := (&stopProcessTool{}).Execute(ctx, ec, map[string]any{"name": "long"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for p.Alive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if p.Alive() {
		t.Error("止めたのに走り続けている")
	}
	if _, ok := ec.Procs.Get("s1", "long"); ok {
		t.Error("止めたものが残っている")
	}
}

// 会話を消したのに、その会話が走らせたものだけが残ってはならない。
func TestCloseSessionStopsAll(t *testing.T) {
	ec := procCtx(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b"} {
		if _, err := (&startProcessTool{}).Execute(ctx, ec, map[string]any{
			"command": sleepCmd(30), "name": n, "wait_ms": 100,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ec.Procs.CloseSession("s1")
	if names := ec.Procs.Names("s1"); len(names) != 0 {
		t.Errorf("残っている: %v", names)
	}
}

// 名前を取り違えたまま何度も呼ばせない。いま何が走っているかを添える。
func TestUnknownProcessNamesTheLiveOnes(t *testing.T) {
	ec := procCtx(t)
	ctx := context.Background()
	if _, err := (&startProcessTool{}).Execute(ctx, ec, map[string]any{
		"command": sleepCmd(30), "name": "alive", "wait_ms": 100,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := (&readProcessTool{}).Execute(ctx, ec, map[string]any{"name": "typo"})
	if err == nil {
		t.Fatal("知らない名前が通っている")
	}
	if !strings.Contains(err.Error(), "alive") {
		t.Errorf("走っているものが示されていない: %v", err)
	}
}

// 走らせたままのものが積み上がると、機械の資源を静かに食い潰す。
func TestProcessLimit(t *testing.T) {
	ec := procCtx(t)
	ctx := context.Background()
	for i := 0; i < maxProcs; i++ {
		if _, err := (&startProcessTool{}).Execute(ctx, ec, map[string]any{
			"command": sleepCmd(30), "name": "p" + string(rune('a'+i)), "wait_ms": 50,
		}); err != nil {
			t.Fatalf("%d 件目: %v", i+1, err)
		}
	}
	_, err := (&startProcessTool{}).Execute(ctx, ec, map[string]any{
		"command": sleepCmd(30), "name": "over", "wait_ms": 50,
	})
	if err == nil {
		t.Fatal("上限を超えて起動できている")
	}
}
