package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

// tickCmd は 1 秒ごとに 1 行出すコマンド。出力は出るが、そのほとんどの時間は
// 眠っている — 「ゆっくりでも進んでいる」を表す。
func tickCmd(n int) string {
	if runtime.GOOS == "windows" {
		// ping は 1 回目を即返すので、1 秒待つには 2 回打つ。
		return "for /l %i in (1,1," + itoa(n) + ") do @(echo tick & ping -n 2 127.0.0.1 >nul)"
	}
	return "i=0; while [ $i -lt " + itoa(n) + " ]; do echo tick; sleep 1; i=$((i+1)); done"
}

// busyCmd は何も出さずに CPU を回すコマンド。「無言でも働いている」を表す。
func busyCmd(sec int) string {
	if runtime.GOOS == "windows" {
		return `powershell -NoProfile -NonInteractive -Command ` +
			`"$e=(Get-Date).AddSeconds(` + itoa(sec) + `); while((Get-Date) -lt $e){}"`
	}
	return "e=$(( $(date +%s) + " + itoa(sec) + " )); while [ $(date +%s) -lt $e ]; do :; done"
}

// spawnCmd は、シェルの子として孫を起こし、待ってからファイルを書かせる。
//
// シェルだけを殺すと孫は生き残り、時間が来たところでファイルを書く。木ごと
// 止められていれば、ファイルは現れない。
func spawnCmd(path string, sec int) string {
	if runtime.GOOS == "windows" {
		return `powershell -NoProfile -NonInteractive -Command ` +
			`"Start-Sleep -Seconds ` + itoa(sec) + `; Set-Content -LiteralPath '` + path + `' -Value alive"`
	}
	return `sh -c 'sleep ` + itoa(sec) + `; echo alive > "` + path + `"' & wait`
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

// 本当に止まっているものは打ち切る。眠っているだけのコマンドは、出力も
// 仕事量も進まない。
//
// 打ち切ったときも、そこまでの出力は返す。何も返さずに止めると、長く走った
// 末に何が起きていたのか分からない。
func TestRunCommandCutsWhenNothingMoves(t *testing.T) {
	// 木ごと止めるようになったので、作業場所は一時ディレクトリでよい。
	// 以前はシェルの先が残って掴み続けるため、消さない場所を使っていた。
	ec := newCtx(t)
	ec.CommandIdle = 500 * time.Millisecond

	start := time.Now()
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": sleepCmd(20),
	})
	if err != nil {
		t.Fatalf("打ち切りが失敗として返っています: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("打ち切られていません: %v", elapsed)
	}
	if !strings.Contains(out, "打ち切りました") {
		t.Errorf("打ち切りが示されていません: %q", out)
	}
	// 何が起きたのかと、次に取れる手を書く。設定画面のラベルをそのまま
	// 名指しするのは、直す場所を探させないためである。
	if !strings.Contains(out, "コマンドの無音の上限") {
		t.Errorf("直し方が示されていません: %q", out)
	}
}

// 総時間の上限は無い。ゆっくりでも出力が出ていれば、いつまでも待つ。
//
// これが無かったために、npm install のようなものが正しく進んでいる途中で
// 切られていた (#470913)。
func TestRunCommandIsNotCutWhileOutputFlows(t *testing.T) {
	ec := newCtx(t)
	// 全体の実行時間は無音の上限をゆうに超えるが、1 秒ごとに動いている。
	ec.CommandIdle = 2 * time.Second

	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": tickCmd(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "打ち切りました") {
		t.Fatalf("進んでいるのに打ち切られました: %q", out)
	}
	if n := strings.Count(out, "tick"); n != 5 {
		t.Errorf("最後まで走っていません: tick が %d 回 (%q)", n, out)
	}
}

// 出力を出さなくても、仕事をしていれば打ち切らない。
//
// 出力だけで判じると、無言で数分走るビルドを殺す。木全体の仕事量を見て
// いるので、黙って計算しているものは生き残る。
func TestRunCommandIsNotCutWhileWorking(t *testing.T) {
	ec := newCtx(t)
	ec.CommandIdle = 1500 * time.Millisecond

	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": busyCmd(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "打ち切りました") {
		t.Fatalf("働いているのに打ち切られました: %q", out)
	}
}

// 利用者の中断は、止まっていたことにしない。どちらも ctx の取り消しとして
// 届くので、見分けて書き分けないと中断したのに「止まっていた」と言う。
func TestRunCommandTellsAbortFromStall(t *testing.T) {
	ec := newCtx(t)
	ec.CommandIdle = 30 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()
	out, err := (&runCommandTool{}).Execute(ctx, ec, map[string]any{
		"command": sleepCmd(20),
	})
	if err != nil {
		t.Fatalf("中断が失敗として返っています: %v", err)
	}
	if !strings.Contains(out, "中断しました") {
		t.Errorf("中断だと示されていません: %q", out)
	}
	if strings.Contains(out, "打ち切りました") {
		t.Errorf("中断を打ち切りとして報告しています: %q", out)
	}
}

// 実行中の出力が、終わる前に流れてくる。
func TestRunCommandStreamsWhileRunning(t *testing.T) {
	var mu sync.Mutex
	var got []string
	ec := newCtx(t)
	ec.Output = func(chunk string) {
		mu.Lock()
		got = append(got, chunk)
		mu.Unlock()
	}

	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": tickCmd(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	streamed := strings.Join(got, "")
	mu.Unlock()

	// 途中で流れていること。1 件にまとまっていても、終わる前に出ていれば
	// 目的は果たしている。
	if !strings.Contains(streamed, "tick") {
		t.Errorf("流れていません: %q", streamed)
	}
	// 流すのは画面のためだけで、モデルへ渡す結果は今までどおり全部を持つ。
	if n := strings.Count(out, "tick"); n != 3 {
		t.Errorf("最終的な結果が変わっています: %q", out)
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

// 打ち切りは木ごと止める。
//
// シェルだけを殺すと、実際に仕事をしているものが親を失って残る。出力の口を
// 掴んだままなので見張りが餌をもらい続け、いつまでも打ち切れない。以前は
// ここが塞がっておらず、試験も「孫が作業場所を掴み続ける」ことを前提に
// 一時ディレクトリを避けていた (#470913)。
func TestRunCommandKillsTheWholeTree(t *testing.T) {
	ec := newCtx(t)
	ec.CommandIdle = 500 * time.Millisecond
	mark := filepath.Join(ec.Workspace, "alive.txt")

	if _, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		"command": spawnCmd(mark, 3),
	}); err != nil {
		t.Fatal(err)
	}

	// 孫が書くはずだった時刻を過ぎるまで待つ。
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(mark); err == nil {
		t.Error("孫が生き残って書き込みました")
	}
}
