//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setGroup は POSIX でのみ意味を持つ。Windows では木ごと殺す手段が別にある。
func setGroup(cmd *exec.Cmd) {}

// setShellLine はシェルへ渡す 1 行を、そのままの形で渡す。
//
// os/exec は引数ごとに引用符を足して 1 本の命令行を組み立てる。cmd はその
// 組み立て方に従わないので、引用符を含むコマンド (git commit -m "..." など) は
// エスケープされた記号ごと相手に届く。同じ 1 行が Windows でだけ壊れる。
//
// /s を付けたうえで全体を引用符で囲うと、cmd は外側の 1 組だけを外し、
// 残りを書いたとおりに解釈する。
func setShellLine(cmd *exec.Cmd, line string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
}

// killTree はシェルとその先で走っているものをまとめて止める。
//
// シェルだけを殺すと、実際に仕事をしているものが親を失って残る。Windows には
// プロセスグループを辿って殺す標準の手段が無いため、OS の道具を借りる。
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
		// 道具が無い、既に終わっている、といった場合は自分だけでも殺す。
		_ = cmd.Process.Kill()
	}
}
