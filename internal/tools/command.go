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

// コマンドの出力の上限。長い出力をそのまま返すと、1 回の実行で文脈を
// 使い切る。切ったことは結果に示す。
const maxCommandOutput = 24 << 10

type runCommandTool struct{}

func (t *runCommandTool) Name() string { return "run_command" }
func (t *runCommandTool) Description() string {
	return "コマンドを実行し、出力と終了コードを返す。ビルド・試験・git など、" +
		"手元の道具を使う仕事はこれで行う。対話を求めるコマンドは応答できないので使わないこと。"
}
func (t *runCommandTool) Parameters() map[string]any {
	return schema(map[string]any{
		"command": strProp("実行するコマンド。シェル越しに走るので、パイプやリダイレクトも書ける。"),
		"cwd":     strProp("実行する場所。省略すると作業ディレクトリ。"),
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

	timeout := ec.CommandTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, flag := shell()
	cmd := exec.CommandContext(ctx, name, flag, line)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	// 上限に達したときに殺せるのはシェルだけで、その先で走っているものは
	// 出力の口を掴んだまま残る。待ち続けると上限が効かないので、猶予を置いて
	// 口を閉じ、そこまでの出力を持って戻る。
	cmd.WaitDelay = 2 * time.Second

	runErr := cmd.Run()

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
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("%s\n\n(%s で打ち切りました)", b.String(), timeout), nil
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
