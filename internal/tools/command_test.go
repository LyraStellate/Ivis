package tools

import (
	"context"
	"os"
	"runtime"
	"strconv"
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

func itoa(n int) string { return strconv.Itoa(n) }

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

// 長い出力をそのまま返すと 1 回の実行でコンテキストを使い切る。
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

// 引用符を含むコマンドが、書いたとおりに届くこと。
//
// os/exec は Windows で引数ごとに引用符を足して命令行を組み立てるが、cmd は
// その組み立て方に従わない。素通しにすると git commit -m "..." のような、
// 最もよく書かれる形が壊れた記号ごと相手に渡る。
func TestRunCommandKeepsQuotes(t *testing.T) {
	ec := newCtx(t)
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": `echo "a b"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `\"`) {
		t.Errorf("引用符が壊れている: %q", out)
	}
	if !strings.Contains(out, "a b") {
		t.Errorf("出力 = %q", out)
	}
}

// 対話を求めるコマンドへ、先に答えを渡せること。
//
// 何も繋がないと、入力を求めるものは読めるものを持たないまま失敗する。
// Windows の date のような、対話を意図していない組み込みコマンドでも同じ
// ことが起きる。
func TestRunCommandFeedsStdin(t *testing.T) {
	ec := newCtx(t)
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": "sort",
		"stdin":   "beta\nalpha\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("標準入力が届いていない: %q", out)
	}
	if strings.Index(out, "alpha") > strings.Index(out, "beta") {
		t.Errorf("並べ替えられていない: %q", out)
	}
}
