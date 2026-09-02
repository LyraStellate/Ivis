package httpapi

import (
	"net/http"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/skillreg"
)

type statusBody struct {
	Provider     string               `json:"provider"`
	ProviderOK   bool                 `json:"provider_ok"`
	ProviderErr  string               `json:"provider_error,omitempty"`
	Workspace    string               `json:"workspace"`
	ConfigPath   string               `json:"config_path"`
	DefaultAgent string               `json:"default_agent"`
	AgentErrors  []agent.LoadError    `json:"agent_errors"`
	SkillErrors  []skillreg.LoadError `json:"skill_errors"`
	Conflicts    []skillreg.Conflict  `json:"skill_conflicts"`
}

// handleStatus は起動状態をまとめて返す。読み込みに失敗した定義や衝突した
// スキルをここで出さないと、利用者は「編集したのに反映されない」理由に
// 辿り着けない。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	body := statusBody{
		Provider:     s.prov.Name(),
		ProviderOK:   true,
		Workspace:    s.cfg.WorkspaceDir,
		ConfigPath:   s.cfg.Path(),
		DefaultAgent: s.cfg.DefaultAgent,
		AgentErrors:  s.agents.Errors(),
		SkillErrors:  s.skills.Errors(),
		Conflicts:    s.skills.Conflicts(),
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
	s.agents.Load(s.cfg.AgentPaths)
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
		OllamaBaseURL      string   `json:"ollama_base_url"`
		DefaultAgent       string   `json:"default_agent"`
		AgentPaths         []string `json:"agent_paths"`
		SkillPaths         []string `json:"skill_paths"`
		WorkspaceDir       string   `json:"workspace_dir"`
		MaxIterations      int      `json:"max_iterations"`
		MaxDelegationDepth int      `json:"max_delegation_depth"`
		ScriptTimeoutSec   int      `json:"script_timeout_sec"`
		RequireApproval    *bool    `json:"require_approval"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
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
	if in.RequireApproval != nil {
		s.cfg.RequireApproval = *in.RequireApproval
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
	s.agents.Load(s.cfg.AgentPaths)
	s.skills.Load(s.cfg.SkillPaths)
	writeJSON(w, http.StatusOK, s.cfg)
}
