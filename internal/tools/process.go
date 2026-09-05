package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// 1 つの会話が同時に走らせておけるプロセスの数。上限を置くのは、起動して
// 放置されたものが積み上がると、機械の資源を静かに食い潰すためである。
const maxProcs = 8

// 読み取られないまま溜めておく出力の上限。超えた分は古い側から捨てる。
// 捨てたことは読み取り時に示す。伸び続ける出力を全部持つと、起動しっぱなしの
// サーバー 1 つで記憶を使い切る。
const maxProcBuffer = 64 << 10

// 読み取りが「出力が途切れた」と判断するまでの静けさ。
const procSettle = 250 * time.Millisecond

// 読み取りの待ち時間の既定と上限。
const (
	procWaitDefault = 2 * time.Second
	procWaitMax     = 60 * time.Second
)

// Proc は走らせたままにしてあるプロセス 1 つ。
//
// run_command と違い、これはツール呼び出しをまたいで生き続ける。対話を
// 求めるコマンド (REPL、input() を持つスクリプト、ビルドの監視) は、
// 起動と入力と読み取りが別々の呼び出しにならないと扱えない。
type Proc struct {
	Name    string
	Command string
	Dir     string

	cmd   *exec.Cmd
	stdin *os.File
	// done は終了で閉じる。生きているかの判定に使う。
	done chan struct{}

	mu sync.Mutex
	// buf はまだ読み取られていない出力。標準出力と標準エラーを 1 本に
	// まとめて受けるので、実際に出た順のまま残る。
	buf []byte
	// dropped は上限を超えて捨てた分があること。
	dropped bool
	// last は最後に出力があった時刻。途切れたかの判定に使う。
	last time.Time
	// status は終了したときの結果。生きている間は空。
	status string
}

// Alive はまだ走っているかを返す。
func (p *Proc) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Status は終了の結果を返す。走っている間は空。
func (p *Proc) Status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// pump は出力を溜め続ける。読み取りを待たずに吸い出すのは、パイプが詰まると
// 相手が書き込みで止まり、こちらが読むまで先へ進まなくなるためである。
func (p *Proc) pump(r *os.File) {
	defer r.Close()
	buf := make([]byte, 8<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.mu.Lock()
			p.buf = append(p.buf, buf[:n]...)
			if len(p.buf) > maxProcBuffer {
				p.buf = p.buf[len(p.buf)-maxProcBuffer:]
				p.dropped = true
			}
			p.last = time.Now()
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// take は溜まっている出力を取り出して空にする。
func (p *Proc) take() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buf) == 0 {
		return "", p.dropped
	}
	// 文字にするのは取り出す時点でまとめて行う。届いた片ごとに変換すると、
	// 多バイト文字の途中で切れた片を化けたものとして確定させてしまう。
	s := decodeConsole(p.buf)
	dropped := p.dropped
	p.buf = nil
	p.dropped = false
	// 読み取った時点で「最後に出た時刻」も忘れる。残したままにすると、
	// 次の読み取りが以前の出力を根拠に「もう途切れている」と判断し、
	// これから出るはずの応答を待たずに戻る。
	p.last = time.Time{}
	return s, dropped
}

// quiet は前回の読み取りより後に出た出力が、途切れてからの時間を返す。
// まだ何も来ていなければ false。
func (p *Proc) quiet() (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last.IsZero() {
		return 0, false
	}
	return time.Since(p.last), true
}

// pending は溜まっている出力の量を返す。
func (p *Proc) pending() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buf)
}

// Wait は出力が途切れるか、待ち時間が尽きるか、プロセスが終わるまで待つ。
//
// 出力が出た「あと」の静けさを待つのは、対話するコマンドが問いを書き終える
// までに間があるためである。書き終える前に読むと、問いの途中だけを読んで
// 答えることになる。
func (p *Proc) Wait(ctx context.Context, d time.Duration) {
	deadline := time.Now().Add(d)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if q, ok := p.quiet(); ok && q >= procSettle {
			return
		}
		if !p.Alive() && p.pending() > 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			// 終わった直後の書き込みが残っていることがある。少しだけ待つ。
			time.Sleep(procSettle)
			return
		case <-tick.C:
			if time.Now().After(deadline) {
				return
			}
		}
	}
}

// Write は標準入力へ書く。行として渡らないと相手が読み終えないため、
// 改行で終わっていなければ足す。
func (p *Proc) Write(s string) error {
	if !p.Alive() {
		return fmt.Errorf("プロセス %q は既に終了しています (%s)", p.Name, p.Status())
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if _, err := p.stdin.WriteString(s); err != nil {
		return fmt.Errorf("標準入力へ書けませんでした: %v", err)
	}
	return nil
}

// stop は終わらせる。シェル越しに起動しているので、その先で走っているものも
// まとめて止める。シェルだけを殺すと、実際に仕事をしているものが残る。
func (p *Proc) stop() {
	killTree(p.cmd)
	p.stdin.Close()
}

// ProcSet は走らせたままのプロセスを会話ごとに束ねる。
//
// 会話をまたいで共有しないのは、名前が衝突するためだけではない。ある会話で
// 起動したものを別の会話から止められると、何が動いているかを誰も把握できない。
type ProcSet struct {
	mu    sync.Mutex
	procs map[string]map[string]*Proc
}

// NewProcSet は空の集合を返す。
func NewProcSet() *ProcSet {
	return &ProcSet{procs: map[string]map[string]*Proc{}}
}

// Start はプロセスを起動する。名前が既に使われていれば断る。
func (s *ProcSet) Start(session, name, line, dir string) (*Proc, error) {
	s.mu.Lock()
	box := s.procs[session]
	if box == nil {
		box = map[string]*Proc{}
		s.procs[session] = box
	}
	// 終わったものは名前を明け渡す。読み取られる前に消さないのは、
	// 終了間際の出力を取り落とさないためである。
	if old, ok := box[name]; ok {
		if old.Alive() {
			s.mu.Unlock()
			return nil, fmt.Errorf("プロセス %q は既に走っています。別の名前を使うか stop_process で止めてください", name)
		}
		if old.pending() > 0 {
			s.mu.Unlock()
			return nil, fmt.Errorf("プロセス %q の出力がまだ読まれていません。read_process で読んでから起動してください", name)
		}
		delete(box, name)
	}
	if len(box) >= maxProcs {
		s.mu.Unlock()
		return nil, fmt.Errorf("走らせたままのプロセスが上限 (%d) に達しました。使い終わったものを stop_process で止めてください", maxProcs)
	}
	s.mu.Unlock()

	shellName, flag := shell()
	// 会話の終わりに縛らない。ツール呼び出しをまたいで生きることが目的なので、
	// 起動した呼び出しの文脈で殺してはならない。止めるのは stop か会話の削除。
	cmd := exec.Command(shellName, flag, line)
	setShellLine(cmd, line)
	cmd.Dir = dir

	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	cmd.Stdin = inR
	// 1 本のパイプへまとめる。別々に受けると、出た順が復元できない。
	cmd.Stdout = outW
	cmd.Stderr = outW
	setGroup(cmd)

	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, fmt.Errorf("起動できませんでした: %v", err)
	}
	// 子へ渡した側は親では閉じる。閉じないと、相手が終わっても読み取りが
	// 終わらない。
	inR.Close()
	outW.Close()

	p := &Proc{Name: name, Command: line, Dir: dir,
		cmd: cmd, stdin: inW, done: make(chan struct{})}
	go p.pump(outR)
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if err == nil {
			p.status = "終了コード 0"
		} else if ee, ok := err.(*exec.ExitError); ok {
			p.status = fmt.Sprintf("終了コード %d", ee.ExitCode())
		} else {
			p.status = err.Error()
		}
		p.mu.Unlock()
		close(p.done)
	}()

	s.mu.Lock()
	s.procs[session][name] = p
	s.mu.Unlock()
	return p, nil
}

// Get は名前で引く。
func (s *ProcSet) Get(session, name string) (*Proc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[session][name]
	return p, ok
}

// Names はその会話で起動したものの名前を返す。
func (s *ProcSet) Names(session string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.procs[session]))
	for n := range s.procs[session] {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Stop は 1 つ止めて片付ける。既に終わっていても片付けは行う。
func (s *ProcSet) Stop(session, name string) (*Proc, bool) {
	s.mu.Lock()
	p, ok := s.procs[session][name]
	if ok {
		delete(s.procs[session], name)
	}
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	p.stop()
	return p, true
}

// CloseSession はその会話のものをすべて止める。会話を消したのに、その会話が
// 起動したものだけが残るという状態を作らない。
func (s *ProcSet) CloseSession(session string) {
	s.mu.Lock()
	box := s.procs[session]
	delete(s.procs, session)
	s.mu.Unlock()
	for _, p := range box {
		p.stop()
	}
}

// Close はすべて止める。終了時に呼ぶ。
func (s *ProcSet) Close() {
	s.mu.Lock()
	all := s.procs
	s.procs = map[string]map[string]*Proc{}
	s.mu.Unlock()
	for _, box := range all {
		for _, p := range box {
			p.stop()
		}
	}
}

// waitFor は引数から待ち時間を読む。
func waitFor(args map[string]any) time.Duration {
	ms := argInt(args, "wait_ms")
	if ms <= 0 {
		return procWaitDefault
	}
	d := time.Duration(ms) * time.Millisecond
	if d > procWaitMax {
		return procWaitMax
	}
	return d
}

// report は読み取った結果を文にする。出力が無いことと終わったことは、
// どちらもモデルが次の手を決めるのに要る。
func report(p *Proc, out string, dropped bool) string {
	var b strings.Builder
	if dropped {
		b.WriteString("(古い出力が上限を超えたため、一部を捨てました)\n")
	}
	if s := strings.TrimRight(out, "\n"); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	} else {
		b.WriteString("(新しい出力はありません)\n")
	}
	b.WriteString("\n")
	if p.Alive() {
		fmt.Fprintf(&b, "(%s は実行中です)", p.Name)
	} else {
		fmt.Fprintf(&b, "(%s は終了しました。%s)", p.Name, p.Status())
	}
	return b.String()
}

// procOf は名前からプロセスを引く。見つからないときは、いま何が走っているかを
// 添えて返す。名前を取り違えたまま何度も呼ばせないため。
func procOf(ec *ExecContext, args map[string]any) (*Proc, error) {
	if ec.Procs == nil {
		return nil, fmt.Errorf("この環境ではプロセスを走らせたままにできません")
	}
	name, err := argString(args, "name")
	if err != nil {
		return nil, err
	}
	p, ok := ec.Procs.Get(ec.Session, name)
	if !ok {
		live := ec.Procs.Names(ec.Session)
		if len(live) == 0 {
			return nil, fmt.Errorf("プロセス %q はありません。走っているものはありません", name)
		}
		return nil, fmt.Errorf("プロセス %q はありません。いまあるのは %s です",
			name, strings.Join(live, ", "))
	}
	return p, nil
}

type startProcessTool struct{}

func (t *startProcessTool) Name() string { return "start_process" }
func (t *startProcessTool) Description() string {
	return "コマンドを走らせたまま残し、あとから入力と読み取りができるようにする。" +
		"対話を求めるもの (REPL、input() を持つスクリプト)、終わらないもの (サーバー、監視) に使う。" +
		"終われば結果が返るコマンドは run_command で足りる。"
}
func (t *startProcessTool) Parameters() map[string]any {
	return schema(map[string]any{
		"command": strProp("走らせるコマンド。シェル越しに走る。"),
		"name":    strProp("このプロセスに付ける名前。以後の読み書きで使う。省略すると自動で付ける。"),
		"cwd":     strProp("走らせる場所。省略すると作業ディレクトリ。"),
		"wait_ms": map[string]any{
			"type":        "integer",
			"description": "起動直後の出力を待つ時間 (ミリ秒)。既定 2000。",
		},
	}, "command")
}

// 起動は取り消せない。run_command と同じ扱いにする。
func (t *startProcessTool) NeedsApproval() bool { return true }

func (t *startProcessTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	if ec.Procs == nil {
		return "", fmt.Errorf("この環境ではプロセスを走らせたままにできません")
	}
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
	name := strings.TrimSpace(argStringOpt(args, "name"))
	if name == "" {
		name = nextProcName(ec)
	}

	p, err := ec.Procs.Start(ec.Session, name, line, dir)
	if err != nil {
		return "", err
	}
	p.Wait(ctx, waitFor(args))
	out, dropped := p.take()
	return fmt.Sprintf("%s として起動しました。\n\n%s", name, report(p, out, dropped)), nil
}

// nextProcName は使われていない名前を作る。
func nextProcName(ec *ExecContext) string {
	used := map[string]bool{}
	for _, n := range ec.Procs.Names(ec.Session) {
		used[n] = true
	}
	for i := 1; ; i++ {
		n := fmt.Sprintf("p%d", i)
		if !used[n] {
			return n
		}
	}
}

type readProcessTool struct{}

func (t *readProcessTool) Name() string { return "read_process" }
func (t *readProcessTool) Description() string {
	return "走らせたままのプロセスの、前回から後に出た分を読む。" +
		"出力が途切れるか待ち時間が尽きるまで待つ。"
}
func (t *readProcessTool) Parameters() map[string]any {
	return schema(map[string]any{
		"name": strProp("start_process で付けた名前。"),
		"wait_ms": map[string]any{
			"type":        "integer",
			"description": "出力を待つ時間 (ミリ秒)。既定 2000、上限 60000。",
		},
	}, "name")
}

func (t *readProcessTool) NeedsApproval() bool { return false }

func (t *readProcessTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	p, err := procOf(ec, args)
	if err != nil {
		return "", err
	}
	p.Wait(ctx, waitFor(args))
	out, dropped := p.take()
	return report(p, out, dropped), nil
}

type writeProcessTool struct{}

func (t *writeProcessTool) Name() string { return "write_process" }
func (t *writeProcessTool) Description() string {
	return "走らせたままのプロセスの標準入力へ 1 行送り、それに対する出力を読む。" +
		"問いに答える、REPL に式を渡す、といった用途に使う。"
}
func (t *writeProcessTool) Parameters() map[string]any {
	return schema(map[string]any{
		"name":  strProp("start_process で付けた名前。"),
		"input": strProp("送る文字列。改行は自動で付く。"),
		"wait_ms": map[string]any{
			"type":        "integer",
			"description": "送ったあと出力を待つ時間 (ミリ秒)。既定 2000、上限 60000。",
		},
	}, "name", "input")
}

// 走らせたままのシェルへ書けることは、コマンドを実行できることと変わらない。
// 起動だけを確認して以後を素通しにすると、承認の線引きが意味を失う。
func (t *writeProcessTool) NeedsApproval() bool { return true }

func (t *writeProcessTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	p, err := procOf(ec, args)
	if err != nil {
		return "", err
	}
	in, err := argString(args, "input")
	if err != nil {
		return "", err
	}
	// 送る前に溜まっている分を流す。前の問いへの答えと、いま送ったものへの
	// 応答が混ざると、どちらに対する出力か分からなくなる。
	p.take()
	if err := p.Write(in); err != nil {
		return "", err
	}
	p.Wait(ctx, waitFor(args))
	out, dropped := p.take()
	return report(p, out, dropped), nil
}

type stopProcessTool struct{}

func (t *stopProcessTool) Name() string { return "stop_process" }
func (t *stopProcessTool) Description() string {
	return "走らせたままのプロセスを止める。使い終わったら必ず止めること。"
}
func (t *stopProcessTool) Parameters() map[string]any {
	return schema(map[string]any{
		"name": strProp("start_process で付けた名前。"),
	}, "name")
}

// 止めることは誰にとっても安全なので確認しない。暴走を止める操作に確認を
// 挟むと、止めたいときに止まらない。
func (t *stopProcessTool) NeedsApproval() bool { return false }

func (t *stopProcessTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	p, err := procOf(ec, args)
	if err != nil {
		return "", err
	}
	out, dropped := p.take()
	ec.Procs.Stop(ec.Session, p.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "%s を止めました。\n", p.Name)
	if dropped {
		b.WriteString("(古い出力が上限を超えたため、一部を捨てました)\n")
	}
	if s := strings.TrimRight(out, "\n"); s != "" {
		b.WriteString("\n残っていた出力:\n")
		b.WriteString(s)
	}
	return b.String(), nil
}
