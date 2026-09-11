// Package config は Ivis の設定ファイルを読み書きする。
//
// 設定・エージェント定義・スキルはいずれもファイルを正とする。Ivis 側から
// 勝手に書き戻さないため、利用者が手で編集し git で管理できる状態が保たれる。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LyraStellate/Ivis/internal/websearch"
)

// Config は Ivis 全体の設定。ファイル 1 つに収める。
type Config struct {
	// Listen は待ち受けアドレス。単一利用者を前提とし、既定でループバックに束縛する。
	Listen string `json:"listen"`
	// OllamaBaseURL は Ollama の接続先。別マシンの Ollama を指すこともできる。
	OllamaBaseURL string `json:"ollama_base_url"`
	// DefaultAgent は新規セッションで既定に選ばれるエージェント ID。
	DefaultAgent string `json:"default_agent"`
	// AgentPaths はエージェント定義 (*.json) を探すディレクトリ。
	AgentPaths []string `json:"agent_paths"`
	// SkillPaths はスキルを探すディレクトリ。既存の Claude スキル置き場を
	// そのまま指定できることを要件とする。先に書いたパスが優先される。
	SkillPaths []string `json:"skill_paths"`
	// DataDir はセッション履歴のデータベースを置く場所。
	DataDir string `json:"data_dir"`
	// WorkspaceDir はツールがファイルを読み書きしてよい境界。この外へは出さない。
	WorkspaceDir string `json:"workspace_dir"`

	// MaxIterations は 1 ターン内のツール呼び出し反復の上限。
	//
	// これは安全網であって、堂々巡りを止める仕掛けではない。止めるのは
	// MaxRepeats のほうで、そちらは「同じ手を繰り返しているか」を見る。
	// 回数で切ると、正しく進んでいる長い作業まで途中で止まる。
	MaxIterations int `json:"max_iterations"`
	// MaxRepeats は、名前も引数もまったく同じツール呼び出しが続いてよい
	// 回数。ローカルモデルは同じ手を繰り返す癖があり、それは回数ではなく
	// 同一性で見分けられる。
	MaxRepeats int `json:"max_repeats"`
	// MaxDelegationDepth は委譲の深さの上限。
	MaxDelegationDepth int `json:"max_delegation_depth"`
	// MaxTurns はチームセッションの 1 ラウンドで回す手番の上限。互いに
	// 送り合う形に入ると手番は尽きないので、どこかで必ず止める (#640275)。
	MaxTurns int `json:"max_turns"`
	// ContextTokens は 1 回の生成で使うコンテキスト長 (num_ctx)。明示しないと提供元の
	// 既定 (Ollama は 4096) が使われ、ツールの結果を往復するだけで埋まる。
	// 埋まると古い側から黙って捨てられ、指示文ごと失われて応答が途中で終わる。
	// エージェント定義の options に num_ctx があれば、そちらが優先される。
	ContextTokens int `json:"context_tokens"`
	// ScriptIdleSec と CommandIdleSec は、実行が動かないまま待つ上限 (秒)。
	//
	// 実行そのものには時間の上限を置かない。時間で切ると、正しく進んでいる
	// 長い仕事まで止まる — npm install も go build も当たり前に数分かかり、
	// 途中で切れば中途半端に入ったライブラリが残る。止めるべきなのは進んで
	// いないときだけである (#470913)。
	//
	// 「動いている」は、出力が届いたことと、木全体の仕事量 (CPU 時間・I/O)
	// が進んだことの両方で数え直す。だから、無言で計算し続けるビルドも、
	// CPU をほとんど使わない取得も、動いているものとして扱われる。
	//
	// 用途が違えば妥当な長さも違うので、スクリプトとコマンドで別に持つ。
	// 0 以下なら見張らない。
	ScriptIdleSec  int `json:"script_idle_sec"`
	CommandIdleSec int `json:"command_idle_sec"`
	// IdleTimeoutSec は、提供元から何も届かないまま待つ上限 (秒)。
	//
	// 生成そのものに上限は置かない。時間で切ると長い仕事ができなくなる。
	// 上限を置くのは「何も届かない時間」で、これはモデルが考えている間では
	// なく、通路が死んでいる間に伸びる。別の端末の Ollama を VPN 越しに
	// 使うと通路は黙って落ち、落ちたことはどちらの側にも伝わらない。
	IdleTimeoutSec int `json:"idle_timeout_sec"`
	// ProbeTimeoutSec は、生きているかを尋ねる要求の待ち時間 (秒)。
	//
	// 生成の待ち時間とは別に持つ。あちらは長く待ってよいが、こちらは短い
	// ほうがよい。ただし短すぎると、別の端末の Ollama を VPN 越しに使って
	// いるときに、動いている相手を落ちていると判じてしまう。
	ProbeTimeoutSec int `json:"probe_timeout_sec"`
	// HeadTimeoutSec は、使い回した接続で応答の頭を待つ上限 (秒)。
	//
	// 使い回している接続は黙って死んでいることがある。死んだ接続と、考え込んで
	// いる相手は区別できないので、この時間を過ぎたら接続を張り直して送り直す。
	// 張り直しは接続 1 本ぶんの費用しかかからず、死んだ接続を待ち続けるのは
	// IdleTimeoutSec を丸ごと捨てる。
	//
	// 繋ぎ直した接続には掛けない。そちらで頭が遅いのは、モデルの読み込みが
	// 長いだけのことがある。0 以下なら掛けない。
	HeadTimeoutSec int `json:"head_timeout_sec"`
	// RequireApproval が false のとき、承認を必要とするツールを確認なしで実行する。
	RequireApproval bool `json:"require_approval"`
	// AutoApprove に載せたツールは、既定で確認を求めるものであっても
	// 確認せずに実行する。"*" ですべて。ツールが増えるほど確認の回数が
	// 増え、自律駆動が成り立たなくなるため、線引きを利用者が引けるようにする。
	AutoApprove []string `json:"auto_approve"`

	// SearchBackend は Web 検索の取得元。既定は鍵の要らないもの。
	SearchBackend string `json:"search_backend"`
	// SearchAPIKey は鍵の要る取得元へ渡す鍵。
	SearchAPIKey string `json:"search_api_key"`

	// Discord は Discord 連携の設定 (#617204)。
	Discord Discord `json:"discord"`

	// path は読み込み元。保存時に使う。設定ファイル自体には書き出さない。
	path string `json:"-"`
}

// Discord は Discord 連携の設定。
//
// 反応する範囲を絞る項目は持たない。ボットは招待された場所にしか居ないので、
// 招待の管理がその役割を果たす (#617204)。
type Discord struct {
	// Enabled が真のとき Gateway へ接続する。トークンを消さずに止められる
	// ようにするため、有効かどうかはトークンと別に持つ。
	Enabled bool `json:"enabled"`
	// Token はボットのトークン。設定ファイルには平文で載る。
	Token string `json:"token"`
}

// Ready は接続を試みてよいかを返す。
func (d Discord) Ready() bool { return d.Enabled && strings.TrimSpace(d.Token) != "" }

// DefaultPath は設定ファイルの既定の位置を返す。
func DefaultPath() string {
	if v := os.Getenv("IVIS_CONFIG"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".ivis", "config.json")
}

// Default は何も設定がない状態の既定値を返す。
func Default() *Config {
	home := homeDir()
	return &Config{
		Listen:             "127.0.0.1:8317",
		OllamaBaseURL:      "http://127.0.0.1:11434",
		DefaultAgent:       "general",
		AgentPaths:         []string{filepath.Join(home, ".ivis", "agents")},
		SkillPaths:         []string{filepath.Join(home, ".ivis", "skills")},
		DataDir:            filepath.Join(home, ".ivis"),
		WorkspaceDir:       filepath.Join(home, ".ivis", "workspace"),
		MaxIterations:      200,
		MaxRepeats:         3,
		MaxDelegationDepth: 3,
		MaxTurns:           24,
		ContextTokens:      16384,
		ScriptIdleSec:      120,
		CommandIdleSec:     120,
		IdleTimeoutSec:     300,
		ProbeTimeoutSec:    10,
		HeadTimeoutSec:     90,
		AutoApprove:        []string{},
		SearchBackend:      websearch.Backends[0],
		RequireApproval:    true,
	}
}

// Load は設定ファイルを読む。存在しない場合は既定値で新規作成する。
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	cfg := Default()
	cfg.path = path

	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// 初回起動。既定値を書き出して、利用者が手で編集できる状態にする。
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("初期設定の書き出しに失敗しました: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("設定 %s を読めませんでした: %w", path, err)
	default:
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("設定 %s の JSON が壊れています: %w", path, err)
		}
	}

	cfg.path = path
	cfg.normalize()
	return cfg, nil
}

// Save は現在の設定をファイルへ書き出す。設定画面からの更新でのみ呼ぶ。
func (c *Config) Save() error {
	if c.path == "" {
		c.path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, append(b, '\n'), 0o644)
}

// 会話の種類。直列に 1 本ずつ進むか、複数のメンバーが手番を回すか (#731906)。
//
// 出自 (web / discord) とは別の軸として持つ。出自は「どこから来た会話か」、
// 種類は「どう進む会話か」であり、混ぜると Discord から来た会話をチームに
// できるかという問いに答えられなくなる。
const (
	KindSeries = "series"
	KindTeam   = "team"
)

// SeriesDir は直列に進む会話の作業場所を束ねる段の名前。
//
// 実行形態の段を 1 つ挟むのは、この先の並列セッションが別の形の識別子
// (1 つの会話から枝分かれした複数の実行) を持つためである。挟んでおけば、
// 並列を入れるときに直列側のパスを変えずに済む (#903215)。
const SeriesDir = "series"

// TeamDir はチームセッションの作業場所を束ねる段の名前。メンバーは全員
// この 1 つを共有する。1 つの仕事を分担しているのだから、成果物の置き場を
// 分ける理由がない (#731906)。
const TeamDir = "team"

// SessionWorkspace はそのセッションの作業ディレクトリを返す。
//
// 場所は種類と ID から一意に決まる派生物なので、どこにも保存しない。保存
// すると作業ディレクトリの設定を変えたときに食い違う。
func (c *Config) SessionWorkspace(kind, sessionID string) string {
	if sessionID == "" {
		return c.WorkspaceDir
	}
	seg := SeriesDir
	if kind == KindTeam {
		seg = TeamDir
	}
	return filepath.Join(c.WorkspaceDir, seg, sessionID)
}

// teamAgentSeg はチームエージェントを束ねる段の名前。
//
// 作業ディレクトリの TeamDir と字面は同じだが、別の定数にしてある。片方を
// 変えたときにもう片方が黙って付いてくるのは事故である。
const teamAgentSeg = "team"

// TeamAgentPaths はチームエージェントの定義を探すディレクトリ。
//
// 共通の探索パスの下に 1 段掘る。agent.ReadDir はサブディレクトリを読まない
// ので、共通の一覧にチームエージェントが混ざらない。この分離は、その性質の
// 上に乗っている。
func (c *Config) TeamAgentPaths() []string {
	out := make([]string, 0, len(c.AgentPaths))
	for _, p := range c.AgentPaths {
		out = append(out, filepath.Join(p, teamAgentSeg))
	}
	return out
}

// TeamAgentDir は新しいチームエージェントの書き先。先に書いた探索パスが
// 優先される規則に合わせ、書き出しも先頭へ行う。
func (c *Config) TeamAgentDir() string {
	if len(c.AgentPaths) == 0 {
		return ""
	}
	return filepath.Join(c.AgentPaths[0], teamAgentSeg)
}

// LegacySessionAgentsDir は、会話ごとにエージェント定義を置いていた頃の場所。
//
// チームエージェントは全てのチーム会話で共有されるようになったので、ここを
// 読むのは起動時の移行だけである。前の版のデータベースを持ってきた人のために
// 消さずに残す (#731906)。
func (c *Config) LegacySessionAgentsDir(sessionID string) string {
	return filepath.Join(c.DataDir, "sessions", sessionID, "agents")
}

// LegacySessionsDir は移行元をまとめて走査するための親。
func (c *Config) LegacySessionsDir() string {
	return filepath.Join(c.DataDir, "sessions")
}

// Path は設定の読み込み元を返す。
func (c *Config) Path() string { return c.path }

// EnsureDirs は起動に必要なディレクトリを作る。
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.WorkspaceDir} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("ディレクトリ %s を作成できませんでした: %w", d, err)
		}
	}
	// 探索パスは存在しなくてもよい。既定のものだけ用意しておく。
	dirs := append(append([]string{}, c.AgentPaths...), c.SkillPaths...)
	dirs = append(dirs, c.TeamAgentPaths()...)
	for _, d := range dirs {
		if strings.HasPrefix(d, filepath.Join(homeDir(), ".ivis")) {
			_ = os.MkdirAll(d, 0o755)
		}
	}
	return nil
}

// DBPath は履歴データベースの位置を返す。
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "ivis.db") }

// normalize は空欄を既定値で埋め、パスを展開する。設定ファイルを手で書く際に
// 一部のキーだけ書けば済むようにするため。
func (c *Config) normalize() {
	d := Default()
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	if c.OllamaBaseURL == "" {
		c.OllamaBaseURL = d.OllamaBaseURL
	}
	c.OllamaBaseURL = strings.TrimRight(c.OllamaBaseURL, "/")
	if c.DefaultAgent == "" {
		c.DefaultAgent = d.DefaultAgent
	}
	if len(c.AgentPaths) == 0 {
		c.AgentPaths = d.AgentPaths
	}
	if len(c.SkillPaths) == 0 {
		c.SkillPaths = d.SkillPaths
	}
	if c.DataDir == "" {
		c.DataDir = d.DataDir
	}
	if c.WorkspaceDir == "" {
		c.WorkspaceDir = d.WorkspaceDir
	}
	if c.MaxIterations <= 0 {
		c.MaxIterations = d.MaxIterations
	}
	if c.MaxRepeats <= 0 {
		c.MaxRepeats = d.MaxRepeats
	}
	if c.MaxDelegationDepth <= 0 {
		c.MaxDelegationDepth = d.MaxDelegationDepth
	}
	if c.MaxTurns <= 0 {
		c.MaxTurns = d.MaxTurns
	}
	if c.ContextTokens <= 0 {
		c.ContextTokens = d.ContextTokens
	}
	if c.ScriptIdleSec < 0 {
		c.ScriptIdleSec = 0
	}
	if c.ScriptIdleSec == 0 {
		c.ScriptIdleSec = d.ScriptIdleSec
	}
	if c.IdleTimeoutSec < 0 {
		c.IdleTimeoutSec = 0
	}
	if c.IdleTimeoutSec == 0 {
		c.IdleTimeoutSec = d.IdleTimeoutSec
	}
	if c.HeadTimeoutSec < 0 {
		c.HeadTimeoutSec = 0
	}
	if c.HeadTimeoutSec == 0 {
		c.HeadTimeoutSec = d.HeadTimeoutSec
	}
	if c.ProbeTimeoutSec <= 0 {
		c.ProbeTimeoutSec = d.ProbeTimeoutSec
	}
	if c.CommandIdleSec < 0 {
		c.CommandIdleSec = 0
	}
	if c.CommandIdleSec == 0 {
		c.CommandIdleSec = d.CommandIdleSec
	}
	if c.SearchBackend == "" {
		c.SearchBackend = d.SearchBackend
	}
	if c.AutoApprove == nil {
		c.AutoApprove = []string{}
	}

	c.AgentPaths = expandAll(c.AgentPaths)
	c.SkillPaths = expandAll(c.SkillPaths)
	c.DataDir = Expand(c.DataDir)
	c.WorkspaceDir = Expand(c.WorkspaceDir)
}

func expandAll(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		p = Expand(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Expand は先頭の ~ を展開し、絶対パスに揃える。
func Expand(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(homeDir(), p[2:])
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// SkipsApproval はそのツールを確認なしで実行してよいかを返す。
//
// ツールが持つ既定を出発点に、設定の一覧が上書きする。判定をここ 1 か所に
// 置くのは、ツールごとに散らすと、増やしたツールで上書きを忘れるためである。
func (c *Config) SkipsApproval(tool string) bool {
	for _, n := range c.AutoApprove {
		if n == "*" || n == tool {
			return true
		}
	}
	return false
}
