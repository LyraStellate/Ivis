// Package httpapi はブラウザ向けの HTTP API と静的アセットの配信を担う。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"sync"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
)

// Server は API の実装。
type Server struct {
	cfg     *config.Config
	st      *store.Store
	agents  *agent.Set
	skills  *skillreg.Registry
	tools   *tools.Registry
	prov    provider.Provider
	newProv func(baseURL string) provider.Provider
	eng     *engine.Engine
	assets  fs.FS

	mu sync.Mutex
	// running はセッションごとの中断関数。二重生成も防ぐ。
	running map[string]context.CancelFunc
	// pending は承認待ちの応答先。
	pending map[string]chan bool
}

// Deps は Server の構築に必要な部品。
type Deps struct {
	Config *config.Config
	Store  *store.Store
	Agents *agent.Set
	Skills *skillreg.Registry
	Tools  *tools.Registry
	Prov   provider.Provider
	// NewProvider は接続先が変わったときに提供元を作り直すために使う。
	NewProvider func(baseURL string) provider.Provider
	Assets      fs.FS
}

// New は Server を組み立てる。
func New(d Deps) *Server {
	s := &Server{
		cfg:     d.Config,
		st:      d.Store,
		agents:  d.Agents,
		skills:  d.Skills,
		tools:   d.Tools,
		prov:    d.Prov,
		newProv: d.NewProvider,
		assets:  d.Assets,
		running: map[string]context.CancelFunc{},
		pending: map[string]chan bool{},
	}
	s.eng = &engine.Engine{
		Cfg:      d.Config,
		Store:    d.Store,
		Agents:   d.Agents,
		Skills:   d.Skills,
		Tools:    d.Tools,
		Provider: d.Prov,
		Approver: s,
	}
	return s
}

// refreshProvider は現在の設定で提供元を作り直す。
func (s *Server) refreshProvider() {
	if s.newProv == nil {
		return
	}
	s.prov = s.newProv(s.cfg.OllamaBaseURL)
	s.eng.Provider = s.prov
}

// Handler は経路を組んだハンドラを返す。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/agents", s.handleAgents)
	mux.HandleFunc("POST /api/agents", s.handleCreateAgent)
	mux.HandleFunc("PUT /api/agents/{id}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", s.handleDeleteAgent)
	mux.HandleFunc("GET /api/tools", s.handleTools)
	mux.HandleFunc("GET /api/skills", s.handleSkills)
	mux.HandleFunc("POST /api/reload", s.handleReload)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)

	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handlePatchSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/sessions/{id}/messages", s.handleMessages)
	mux.HandleFunc("POST /api/sessions/{id}/messages", s.handleSend)
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/approvals/{id}", s.handleApproval)

	mux.Handle("/", s.staticHandler())
	return mux
}

// writeJSON は JSON 応答を書く。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error string `json:"error"`
	// Kind は利用者が次に何をすべきかを UI が出し分けるための区別。
	Kind string `json:"kind,omitempty"`
	// Field は入力の問題のとき、どの欄を直せばよいか。
	Field string `json:"field,omitempty"`
}

// writeError は失敗を種類とともに返す。まとめて「エラーが発生しました」に
// すると、Ollama の起動忘れなのか設定の誤りなのかを利用者が判断できない。
func writeError(w http.ResponseWriter, status int, err error) {
	body := errorBody{Error: err.Error(), Kind: kindOf(err)}
	var fe *agent.FieldError
	if errors.As(err, &fe) {
		body.Kind, body.Field = "invalid_input", fe.Field
	}
	writeJSON(w, status, body)
}

// 種類の判定は engine が持つ。ストリーム上の失敗と HTTP の失敗で分類が
// 食い違うと、同じ原因が画面上で別物として見える。
func kindOf(err error) string { return engine.KindOf(err) }

// statusFor は失敗を HTTP の状態へ写す。入力の誤りと、存在しないもの、
// それ以外を分けないと、画面は直せる誤りと直せない障害を区別できない。
func statusFor(err error) int {
	var fe *agent.FieldError
	switch {
	case errors.As(err, &fe):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}
