//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// setGroup は自分を長とするプロセスグループで起動させる。こうしておかないと、
// シェルの先で走っているものを後からまとめて止められない。
func setGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// setShellLine は Windows でのみ意味を持つ。sh は os/exec の組み立て方に
// そのまま従うので、引数として渡すだけでよい。
func setShellLine(cmd *exec.Cmd, line string) {}

// killTree はシェルとその先で走っているものをまとめて止める。
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// 負の値はプロセスグループ全体を指す。届かなければ本人だけを殺す。
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
