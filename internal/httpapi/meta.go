package httpapi

import (
	"fmt"
	"net"
	"net/http"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/tools"
	"github.com/LyraStellate/Ivis/internal/websearch"
)

type statusBody struct {
	Provider     string `json:"provider"`
	ProviderOK   bool   `json:"provider_ok"`
	ProviderErr  string `json:"provider_error,omitempty"`
	Workspace    string `json:"workspace"`
	ConfigPath   string `json:"config_path"`
	DefaultAgent string `json:"default_agent"`
	// Colors は話し手に選べる色。画面が独自に持つと、増減したときに
	// 選べる色と実際に出る色がずれる。
	Colors []string `json:"colors"`
	// Tools は登録済みのツール名と、確認を求めるかの既定。画面が名前を持つと、
	// ツールを増減したときに選べるものと実際に動くものがずれる。
	Tools          []toolInfo           `json:"tools"`
	SearchBackends []string             `json:"search_backends"`
	Shell          string               `json:"shell"`
	AgentErrors    []agent.LoadError    `json:"agent_errors"`
	SkillErrors    []skillreg.LoadError `json:"skill_errors"`
	Conflicts      []skillreg.Conflict  `json:"skill_conflicts"`
}

// handleStatus は起動状態をまとめて返す。読み込みに失敗した定義や衝突した
// スキルをここで出さないと、利用者は「編集したのに反映されない」理由に
// 辿り着けない。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	body := statusBody{
		Provider:       s.prov.Name(),
		ProviderOK:     true,
		Workspace:      s.cfg.WorkspaceDir,
		ConfigPath:     s.cfg.Path(),
		DefaultAgent:   s.cfg.DefaultAgent,
		Colors:         agent.Colors,
		Tools:          s.toolInfos(),
		SearchBackends: websearch.Backends,
		Shell:          tools.ShellName(),
		AgentErrors:    s.agents.Errors(),
		SkillErrors:    s.skills.Errors(),
		Conflicts:      s.skills.Conflicts(),
	}
	if err := s.prov.Health(r.Context()); err != nil {
		body.ProviderOK = false
		body.ProviderErr = err.Error()
	}
	if body.AgentErrors == nil {
		body.AgentErrors = []agent.LoadError{}
	}
	if body.SkillErrors == nil {
		body.SkillErrors = []skillreg.LoadError{}
	}
	if body.Conflicts == nil {
		body.Conflicts = []skillreg.Conflict{}
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	list := s.agents.List()
	if list == nil {
		list = []*agent.Agent{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleTools はエージェントに許可できるツールの一覧を返す。画面が名前を
// 持つと、ツールを増減したときに選べるものと実際に動くものがずれる。
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	defs := s.tools.Defs(func(string) bool { return true })
	if defs == nil {
		defs = []provider.ToolDef{}
	}
	writeJSON(w, http.StatusOK, defs)
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	list := s.skills.List()
	if list == nil {
		list = []*skillreg.Skill{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleReload はファイルを読み直す。定義とスキルはファイルが正なので、
// 編集を反映させる手段として明示的な再読込を用意する。
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	// 外から規定エージェントのファイルが消されていれば、ここで補う。
	// 入口が失われるとどのエージェントも呼べない。
	s.reloadAgents()
	s.skills.Load(s.cfg.SkillPaths)
	s.handleStatus(w, r)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.prov.Models(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

// handlePutConfig は設定を書き換えて保存する。設定はファイルが正だが、
// 画面から変えられないと利用者は接続先ひとつ直すのにも editor を開くことになる。
func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Listen             string   `json:"listen"`
		OllamaBaseURL      string   `json:"ollama_base_url"`
		DefaultAgent       string   `json:"default_agent"`
		AgentPaths         []string `json:"agent_paths"`
		SkillPaths         []string `json:"skill_paths"`
		WorkspaceDir       string   `json:"workspace_dir"`
		MaxIterations      int      `json:"max_iterations"`
		MaxDelegationDepth int      `json:"max_delegation_depth"`
		ScriptTimeoutSec   int      `json:"script_timeout_sec"`
		CommandTimeoutSec  int      `json:"command_timeout_sec"`
		RequireApproval    *bool    `json:"require_approval"`
		AutoApprove        []string `json:"auto_approve"`
		SearchBackend      string   `json:"search_backend"`
		SearchAPIKey       *string  `json:"search_api_key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if in.Listen != "" {
		// 形だけ確かめる。ここを通した値で次の起動が失敗すると、画面から
		// 直すこともできなくなる。
		if _, _, err := net.SplitHostPort(in.Listen); err != nil {
			writeError(w, http.StatusBadRequest,
				fmt.Errorf("待ち受けアドレスは 127.0.0.1:8317 のような形で指定してください: %v", err))
			return
		}
		s.cfg.Listen = in.Listen
	}
	if in.OllamaBaseURL != "" {
		s.cfg.OllamaBaseURL = in.OllamaBaseURL
	}
	if in.DefaultAgent != "" {
		s.cfg.DefaultAgent = in.DefaultAgent
	}
	if len(in.AgentPaths) > 0 {
		s.cfg.AgentPaths = in.AgentPaths
	}
	if len(in.SkillPaths) > 0 {
		s.cfg.SkillPaths = in.SkillPaths
	}
	if in.WorkspaceDir != "" {
		s.cfg.WorkspaceDir = config.Expand(in.WorkspaceDir)
	}
	if in.MaxIterations > 0 {
		s.cfg.MaxIterations = in.MaxIterations
	}
	if in.MaxDelegationDepth > 0 {
		s.cfg.MaxDelegationDepth = in.MaxDelegationDepth
	}
	if in.ScriptTimeoutSec > 0 {
		s.cfg.ScriptTimeoutSec = in.ScriptTimeoutSec
	}
	if in.CommandTimeoutSec > 0 {
		s.cfg.CommandTimeoutSec = in.CommandTimeoutSec
	}
	if in.RequireApproval != nil {
		s.cfg.RequireApproval = *in.RequireApproval
	}
	// 空の一覧は「すべて確認する」という意味を持つ。有無で判断すると、
	// 全部を確認へ戻す操作ができない。
	if in.AutoApprove != nil {
		s.cfg.AutoApprove = in.AutoApprove
	}
	if in.SearchBackend != "" {
		s.cfg.SearchBackend = in.SearchBackend
	}
	// 鍵は空にできる必要がある。消したいのに消せないと、取得元を戻せない。
	if in.SearchAPIKey != nil {
		s.cfg.SearchAPIKey = *in.SearchAPIKey
	}

	if err := s.cfg.EnsureDirs(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// 接続先が変わったら提供元を作り直す。作り直さないと、設定画面で直した
	// はずの接続先が次の生成まで効かない。
	s.refreshProvider()
	s.refreshSearch()
	s.agents.Load(s.cfg.AgentPaths)
	s.skills.Load(s.cfg.SkillPaths)
	writeJSON(w, http.StatusOK, s.cfg)
}

// toolInfo は 1 つのツールの、画面が知る必要のある情報。
type toolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Approval はそのツールが既定で確認を求めるか。設定の一覧はこれを上書きする。
	Approval bool `json:"approval"`
}

func (s *Server) toolInfos() []toolInfo {
	out := []toolInfo{}
	for _, n := range s.tools.Names() {
		t, ok := s.tools.Get(n)
		if !ok {
			continue
		}
		out = append(out, toolInfo{Name: n, Description: t.Description(), Approval: t.NeedsApproval()})
	}
	return out
}
