package tools

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sleepCmd は指定秒だけ止まるコマンド。シェルが違うので分ける。
func sleepCmd(sec int) string {
	if runtime.GOOS == "windows" {
		// ping は 1 回目を即返すため、待ちたい秒数より 1 多く打つ。
		return "ping -n " + itoa(sec+1) + " 127.0.0.1 >nul"
	}
	return "sleep " + itoa(sec)
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestRunCommandReturnsOutput(t *testing.T) {
	ec := newCtx(t)
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("出力 = %q", out)
	}
}

// コマンドが失敗することは異常ではなく、モデルが読むべき結果である。
func TestRunCommandFailureIsAResult(t *testing.T) {
	ec := newCtx(t)
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": "exit 3",
	})
	if err != nil {
		t.Fatalf("失敗したコマンドが実行の失敗として扱われています: %v", err)
	}
	if !strings.Contains(out, "終了コード 3") {
		t.Errorf("終了コードが示されていません: %q", out)
	}
}

// 打ち切ったときも、そこまでの出力は返す。何も返さずに止めると、長く走った
// 末に何が起きていたのか分からない。
func TestRunCommandTimesOut(t *testing.T) {
	// 打ち切っても、シェルの先で走っているものは残り、そこを作業場所として
	// 掴み続ける。試験用の一時ディレクトリを指すと後片付けができないので、
	// 消さない場所で走らせる。
	ec := &ExecContext{Workspace: os.TempDir(), Confined: true,
		CommandTimeout: 300 * time.Millisecond}

	start := time.Now()
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": sleepCmd(5),
	})
	if err != nil {
		t.Fatalf("打ち切りが失敗として返っています: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("打ち切られていません: %v", elapsed)
	}
	if !strings.Contains(out, "打ち切りました") {
		t.Errorf("打ち切りが示されていません: %q", out)
	}
}

// 長い出力をそのまま返すと 1 回の実行で文脈を使い切る。
func TestRunCommandTruncatesOutput(t *testing.T) {
	if len(truncate(strings.Repeat("x", maxCommandOutput+100), maxCommandOutput)) <= maxCommandOutput {
		t.Fatal("切り詰めの前提が崩れています")
	}
	ec := newCtx(t)
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": "echo " + strings.Repeat("y", 2000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > maxCommandOutput+512 {
		t.Errorf("出力が切られていません: %d バイト", len(out))
	}
}

// 境界を課すエージェントは、cwd でも外へ出られない。
func TestRunCommandRespectsBoundary(t *testing.T) {
	ec := newCtx(t)
	_, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": "echo hi", "cwd": "../..",
	})
	if err == nil {
		t.Fatal("境界の外で実行できてしまいました")
	}
}
