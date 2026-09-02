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

	// MaxIterations は 1 ターン内のツール呼び出し反復の上限。ローカルモデルは
	// 同じツールを呼び続けることがあり、これがないとターンが終わらない。
	MaxIterations int `json:"max_iterations"`
	// MaxDelegationDepth は委譲の深さの上限。
	MaxDelegationDepth int `json:"max_delegation_depth"`
	// ScriptTimeoutSec はスキル同梱スクリプトの実行時間の上限 (秒)。
	ScriptTimeoutSec int `json:"script_timeout_sec"`
	// RequireApproval が false のとき、承認を必要とするツールを確認なしで実行する。
	RequireApproval bool `json:"require_approval"`

	// path は読み込み元。保存時に使う。設定ファイル自体には書き出さない。
	path string `json:"-"`
}

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
		MaxIterations:      12,
		MaxDelegationDepth: 3,
		ScriptTimeoutSec:   120,
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
	for _, d := range append(append([]string{}, c.AgentPaths...), c.SkillPaths...) {
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
	if c.MaxDelegationDepth <= 0 {
		c.MaxDelegationDepth = d.MaxDelegationDepth
	}
	if c.ScriptTimeoutSec <= 0 {
		c.ScriptTimeoutSec = d.ScriptTimeoutSec
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
