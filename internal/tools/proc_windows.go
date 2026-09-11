//go:build windows

package tools

import (
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
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

// ジョブオブジェクトで木をまとめて掴む (#470913)。
//
// 見張りが要るのは「木全体が仕事をしているか」であって、シェル 1 つの様子では
// ない。GetProcessTimes で見えるのは cmd.exe だけで、その下で走る npm の CPU は
// 見えない。cmd.exe は子を待っているだけなので、いつ見ても 0 に近い。
//
// ジョブなら、木に属する全部の CPU 時間と I/O がまとめて返る。止めるときも
// TerminateJobObject で木ごと落とせるので、taskkill を起動するより確実で速い。
type winTracker struct {
	job windows.Handle
	cmd *exec.Cmd
}

// jobAccounting は JOBOBJECT_BASIC_AND_IO_ACCOUNTING_INFORMATION。
// x/sys/windows が型を持たないので、ここで形だけ写す。
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
	ReadOperationCount        uint64
	WriteOperationCount       uint64
	OtherOperationCount       uint64
	ReadTransferCount         uint64
	WriteTransferCount        uint64
	OtherTransferCount        uint64
}

// jobBasicAndIoAccounting は JOBOBJECTINFOCLASS の
// JobObjectBasicAndIoAccountingInformation。x/sys/windows が
// JobObjectAssociateCompletionPortInformation = 7 を持っており、その次にあたる。
const jobBasicAndIoAccounting = 8

func newTracker() tracker {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		// 掴めなくても実行はできる。仕事量が測れないだけで、見張りは出力で
		// 判じる形に落ちる。
		return &winTracker{}
	}
	return &winTracker{job: job}
}

// adopt は起動した直後に呼ぶ。
//
// 起動と取り込みの間に孫が生まれると、その孫はジョブの外に出る。シェルが命令を
// 解釈して子を起こすまでには間があるので、実際にはまず取りこぼさない。
func (t *winTracker) adopt(cmd *exec.Cmd) {
	t.cmd = cmd
	if t.job == 0 || cmd.Process == nil {
		return
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(t.job, h); err != nil {
		// 取り込めなかった。仕事量は測れないが、実行は続く。
		windows.CloseHandle(t.job)
		t.job = 0
	}
}

func (t *winTracker) work() (uint64, bool) {
	if t.job == 0 {
		return 0, false
	}
	var info jobAccounting
	var n uint32
	err := windows.QueryInformationJobObject(t.job, jobBasicAndIoAccounting,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &n)
	if err != nil {
		return 0, false
	}
	// CPU 時間と I/O の量を 1 つの目盛りにまとめる。増えているかだけを見るので、
	// 単位の違うものを足してよい。CPU を使わずに落としてくるだけの相手も、
	// I/O のほうで動いていると分かる。
	return uint64(info.TotalUserTime) + uint64(info.TotalKernelTime) +
		info.ReadTransferCount + info.WriteTransferCount, true
}

func (t *winTracker) kill() {
	if t.job != 0 {
		if err := windows.TerminateJobObject(t.job, 1); err == nil {
			return
		}
	}
	if t.cmd != nil {
		killTree(t.cmd)
	}
}

func (t *winTracker) close() {
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}
