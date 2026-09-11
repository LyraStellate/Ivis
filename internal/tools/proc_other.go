//go:build !windows

package tools

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// プロセスグループで木をまとめて掴む (#470913)。
//
// 仕事量は /proc から読む。シェル自身の utime/stime を見ても意味が無い —
// シェルは子を待っているだけで、実際に働くのはその先である。cutime/cstime は
// 子を回収した時点でしか増えないので、走っている最中には使えない。
//
// そこでプロセスグループに属する全部の CPU 時間を足す。/proc が無い環境
// (macOS など) では測れないので ok=false を返し、見張りは出力だけで判じる。
type posixTracker struct {
	cmd  *exec.Cmd
	pgid int
}

func newTracker() tracker { return &posixTracker{} }

func (t *posixTracker) adopt(cmd *exec.Cmd) {
	t.cmd = cmd
	if cmd.Process == nil {
		return
	}
	// setGroup で自分を長にしてあるので、pgid は pid と同じになる。取れない
	// 環境でも pid を使えば、少なくとも本人ぶんは数えられる。
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		t.pgid = pgid
		return
	}
	t.pgid = cmd.Process.Pid
}

func (t *posixTracker) work() (uint64, bool) {
	if t.pgid == 0 {
		return 0, false
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	var total uint64
	var found bool
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			// 読んでいる間に終わったもの。数えないだけでよい。
			continue
		}
		pgid, ticks, ok := statTicks(raw)
		if !ok || pgid != t.pgid {
			continue
		}
		total += ticks
		found = true
	}
	return total, found
}

// statTicks は /proc/<pid>/stat から、プロセスグループと使った CPU を読む。
//
// 2 番目の欄 (実行ファイル名) は括弧で囲まれ、中に空白も括弧も入りうる。
// 頭から数えると壊れるので、最後の ')' より後ろだけを数える。
func statTicks(raw []byte) (pgid int, ticks uint64, ok bool) {
	i := bytes.LastIndexByte(raw, ')')
	if i < 0 {
		return 0, 0, false
	}
	// ')' の次が 3 番目の欄 (状態) になる。
	f := strings.Fields(string(raw[i+1:]))
	// 3 番目からの並びなので、pgrp (5 番目) は f[2]、utime (14) と stime (15)
	// は f[11] と f[12] にあたる。
	if len(f) < 13 {
		return 0, 0, false
	}
	pgid, err := strconv.Atoi(f[2])
	if err != nil {
		return 0, 0, false
	}
	u, err1 := strconv.ParseUint(f[11], 10, 64)
	s, err2 := strconv.ParseUint(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return pgid, u + s, true
}

func (t *posixTracker) kill() {
	if t.cmd != nil {
		killTree(t.cmd)
	}
}

func (t *posixTracker) close() {}
