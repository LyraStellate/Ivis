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
	"github.com/LyraStellate/Ivis/internal/discord"
	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
	"github.com/LyraStellate/Ivis/internal/websearch"
)

// Server は API の実装。
type Server struct {
	cfg    *config.Config
	st     *store.Store
	agents *agent.Set
	// teamAgents はチームセッションでだけ使えるエージェント。共通と別の
	// 一覧にしてあるのは、Tier 0 の扱いと委譲先の一覧が違うためである。
	teamAgents *agent.Set
	skills     *skillreg.Registry
	tools      *tools.Registry
	prov       provider.Provider
	newProv    func(baseURL string) provider.Provider
	eng        *engine.Engine
	assets     fs.FS

	// runs は会話ごとの実行の占有。Discord ブリッジと共有する。共有しないと、
	// 同じ会話が両方の入口から同時に走る (#617204)。
	runs *engine.Runs
	// discord は Discord との連携。設定で無効なときも実体はあり、繋いで
	// いないだけになる。
	discord *discord.Bridge

	// procs は走らせたままのプロセス。会話をまたいで生きるので、実行の
	// 占有ではなくここが持つ。
	procs *tools.ProcSet

	// liveMu と lives は、走っている実行の中継。会話ごとに 1 つで、次の実行が
	// 始まれば置き換わる。画面が離れても実行は続くので、要求ではなくここが
	// 経過を持つ。
	liveMu sync.Mutex
	lives  map[string]*live

	mu sync.Mutex
	// pending は承認待ちの応答先。
	pending map[string]chan bool
	// asking は問いへの答えの届け先。承認と別に持つのは、返るものが
	// 可否ではなく文だからである。
	asking map[string]chan string
}

// Deps は Server の構築に必要な部品。
type Deps struct {
	Config *config.Config
	Store  *store.Store
	Agents *agent.Set
	// TeamAgents はチームエージェント。呼ぶ側が読み込んで渡す。
	TeamAgents *agent.Set
	Skills     *skillreg.Registry
	Tools      *tools.Registry
	Prov       provider.Provider
	// NewProvider は接続先が変わったときに提供元を作り直すために使う。
	NewProvider func(baseURL string) provider.Provider
	Assets      fs.FS
	// Log は常駐部分の出来事を残す口。黙って失敗すると、繋がらない理由が
	// 利用者にも手元にも残らない。
	Log func(format string, args ...any)
}

// New は Server を組み立てる。
func New(d Deps) *Server {
	s := &Server{
		cfg:        d.Config,
		st:         d.Store,
		agents:     d.Agents,
		teamAgents: d.TeamAgents,
		skills:     d.Skills,
		tools:      d.Tools,
		prov:       d.Prov,
		newProv:    d.NewProvider,
		assets:     d.Assets,
		runs:       engine.NewRuns(),
		procs:      tools.NewProcSet(),
		lives:      map[string]*live{},
		pending:    map[string]chan bool{},
		asking:     map[string]chan string{},
	}
	s.eng = &engine.Engine{
		Cfg:        d.Config,
		Store:      d.Store,
		Agents:     d.Agents,
		TeamAgents: d.TeamAgents,
		Skills:     d.Skills,
		Tools:      d.Tools,
		Provider:   d.Prov,
		Approver:   s,
		Asker:      s,
		Search:     websearch.New(d.Config.SearchBackend, d.Config.SearchAPIKey),
		Procs:      s.procs,
	}
	s.discord = discord.New(discord.Deps{
		Cfg:    d.Config,
		Store:  d.Store,
		Agents: d.Agents,
		Runs:   s.runs,
		Run: func(ctx context.Context, id, text string, emit engine.Emit) error {
			return s.eng.Run(ctx, id, text, emit)
		},
		Compact: func(ctx context.Context, id, instructions string) (*engine.CompactResult, error) {
			// Discord では経過を出さない。1 文字ごとに投稿を書き換えることに
			// なり、それは待つ側の役に立たない。
			return s.eng.Compact(ctx, id, instructions, nil)
		},
		Log: d.Log,
	})
	return s
}

// Start は待ち受け以外の常駐を始める。いまは Discord への接続だけ。
func (s *Server) Start() { s.discord.Apply(s.cfg.Discord) }

// Close は常駐を止める。走らせたままのプロセスもここで片付ける。残すと、
// Ivis を終えたのにその子だけが動き続ける。
func (s *Server) Close() {
	s.discord.Stop()
	s.procs.Close()
}

// refreshDiscord は現在の設定を接続へ反映する。
func (s *Server) refreshDiscord() { s.discord.Apply(s.cfg.Discord) }

// refreshSearch は現在の設定で検索の取得元を作り直す。作り直さないと、
// 設定画面で入れた鍵が次の起動まで効かない。
func (s *Server) refreshSearch() {
	s.eng.Search = websearch.New(s.cfg.SearchBackend, s.cfg.SearchAPIKey)
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
	mux.HandleFunc("GET /api/commands", s.handleCommands)
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
	// 走っている実行へ繋ぎ直す。ブラウザを更新しても、続きが見える。
	mux.HandleFunc("GET /api/sessions/{id}/stream", s.handleAttach)
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/sessions/{id}/rewind", s.handleRewind)

	// チームセッションの名簿 (#731906)。
	//
	// 作成とコピーだけ会話の下にある。どちらも「定義を作る」と「この会話で
	// 有効にする」を 1 度に行う操作だからで、編集と削除は共有物への操作
	// なので会話 ID の下に置かない。
	mux.HandleFunc("GET /api/sessions/{id}/agents", s.handleRoster)
	mux.HandleFunc("POST /api/sessions/{id}/agents", s.handleCreateTeamAgent)
	mux.HandleFunc("POST /api/sessions/{id}/agents/copy", s.handleCopyAgent)
	mux.HandleFunc("POST /api/sessions/{id}/members", s.handleEnable)
	mux.HandleFunc("GET /api/sessions/{id}/flow", s.handleFlow)
	mux.HandleFunc("PUT /api/team-agents/{id}", s.handleUpdateTeamAgent)
	mux.HandleFunc("DELETE /api/team-agents/{id}", s.handleDeleteTeamAgent)

	// チケット (#189542)。
	mux.HandleFunc("GET /api/sessions/{id}/tickets", s.handleListTickets)
	mux.HandleFunc("POST /api/sessions/{id}/tickets", s.handleCreateTicket)
	mux.HandleFunc("GET /api/sessions/{id}/tickets/{number}", s.handleGetTicket)
	mux.HandleFunc("PATCH /api/sessions/{id}/tickets/{number}", s.handlePatchTicket)
	mux.HandleFunc("DELETE /api/sessions/{id}/tickets/{number}", s.handleDeleteTicket)
	mux.HandleFunc("POST /api/sessions/{id}/tickets/{number}/notes", s.handleAddNote)
	mux.HandleFunc("POST /api/approvals/{id}", s.handleApproval)
	mux.HandleFunc("POST /api/questions/{id}", s.handleAnswer)

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
	case errors.Is(err, store.ErrNotRewindable):
		return http.StatusBadRequest
	case errors.Is(err, ErrNotTeam):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}
