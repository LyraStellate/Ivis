package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// コマンドの出力の上限。長い出力をそのまま返すと、1 回の実行でコンテキストを
// 使い切る。切ったことは結果に示す。
const maxCommandOutput = 24 << 10

type runCommandTool struct{}

func (t *runCommandTool) Name() string { return "run_command" }
func (t *runCommandTool) Description() string {
	return "コマンドを実行し、終わるまで待って出力と終了コードを返す。ビルド・試験・git など、" +
		"手元の道具を使う仕事はこれで行う。問われる内容が先に分かっているなら stdin に答えを書ける。" +
		"出力を見てから答える必要があるもの、終わらないものは start_process を使う。"
}
func (t *runCommandTool) Parameters() map[string]any {
	return schema(map[string]any{
		"command": strProp("実行するコマンド。シェル越しに走るので、パイプやリダイレクトも書ける。"),
		"cwd":     strProp("実行する場所。省略すると作業ディレクトリ。"),
		"stdin":   strProp("標準入力へ流す文字列。問いが複数あるなら改行で区切って並べる。省略すると何も与えない。"),
	}, "command")
}

// 実行は取り消せない。既定では確認を求め、設定で外せるようにする。
func (t *runCommandTool) NeedsApproval() bool { return true }

func (t *runCommandTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	line, err := argString(args, "command")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(line) == "" {
		return "", fmt.Errorf("command が空です")
	}
	dir := ec.Workspace
	if rel := argStringOpt(args, "cwd"); rel != "" {
		if dir, err = resolve(ec.Workspace, rel, ec.Confined); err != nil {
			return "", err
		}
	}

	idle := ec.CommandIdle
	if idle <= 0 {
		idle = 2 * time.Minute
	}
	// 実行そのものに時間の上限は置かない。見張るのは動いていない時間だけで、
	// それは出力と木の仕事量の両方で数え直す (watch.go)。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := newWatch(idle, cancel)
	defer w.stop()

	trk := newTracker()
	defer trk.close()

	name, flag := shell()
	cmd := exec.CommandContext(runCtx, name, flag, line)
	setShellLine(cmd, line)
	setGroup(cmd)
	cmd.Dir = dir
	// 止めるときは木ごと止める。シェルだけを殺すと、実際に仕事をしている
	// ものが親を失って残り、出力の口を掴んだまま生き続ける。掴んだままだと
	// 見張りが餌をもらい続けて、いつまでも打ち切れない。
	cmd.Cancel = func() error {
		trk.kill()
		return nil
	}
	// 標準入力は必ず与える。何も繋がないと、入力を求めるコマンドが読める
	// ものを持たないまま止まるか、読めないまま失敗する。空でも「これ以上
	// 無い」と伝わる形にしておく。
	cmd.Stdin = strings.NewReader(argStringOpt(args, "stdin"))
	// 木ごと止めても、口を閉じるまでには間がある。猶予を置いて、そこまでの
	// 出力を持って戻る。
	cmd.WaitDelay = 2 * time.Second

	// 出力を覗きながら溜める。覗くところで見張りを数え直し、同じ片を画面へ
	// 流す — 見張りと流しは同じ 1 つの経路である。
	live := newLiveOut(ec.Output)
	defer live.close()
	var out, errBuf bytes.Buffer
	cmd.Stdout = &tap{to: &out, seen: w.seen, live: live}
	cmd.Stderr = &tap{to: &errBuf, seen: w.seen, live: live}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("実行できませんでした: %v", err)
	}
	trk.adopt(cmd)
	go pollWork(runCtx, trk, w)
	runErr := cmd.Wait()
	w.stop()

	// 出力はその環境のコードページで書かれていることがある。文字列にする
	// 前に読み直す。
	var b strings.Builder
	if s := strings.TrimRight(decodeConsole(out.Bytes()), "\n"); s != "" {
		b.WriteString(truncate(s, maxCommandOutput))
	}
	if s := strings.TrimRight(decodeConsole(errBuf.Bytes()), "\n"); s != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[stderr]\n")
		b.WriteString(truncate(s, maxCommandOutput))
	}

	// 打ち切ったときも、そこまでの出力は返す。何も返さずに止めると、長く
	// 走った末に何が起きていたのか分からない。
	//
	// 見張りが切ったのか利用者が中断したのかは、どちらも ctx の取り消しとして
	// 届く。見分けて書き分けないと、中断したのに「止まっていた」と言うことに
	// なる。
	if w.expired() {
		return b.String() + "\n\n" + stalled("コマンド", idle, "コマンドの無音の上限", w.blind.Load()), nil
	}
	if ctx.Err() != nil {
		return b.String() + "\n\n(中断しました)", nil
	}

	// コマンドが失敗することは異常ではなく、モデルが読むべき結果である。
	// 実行そのものが成立していれば、終了コードを添えて返す。
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return fmt.Sprintf("%s\n\n(終了コード %d)", b.String(), exitErr.ExitCode()), nil
	}
	if runErr != nil {
		return "", fmt.Errorf("実行できませんでした: %v", runErr)
	}
	if b.Len() == 0 {
		return "(出力はありません。終了コード 0)", nil
	}
	return b.String(), nil
}

// shell は実行に使うシェルを返す。指示文の書き方が環境で変わるため、
// どちらで走っているかはエージェントの指示文にも現れる。
func shell() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "sh", "-c"
}

// ShellName は表示用のシェル名。
func ShellName() string {
	name, _ := shell()
	return name
}
