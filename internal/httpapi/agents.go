package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/LyraStellate/Ivis/internal/agent"
)

// agentBody は画面から受け取る定義。読み込み元のファイルと Fixed は Ivis 側で
// 決めるものなので受け取らない。
type agentBody struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Model        string         `json:"model"`
	Instructions string         `json:"instructions"`
	Tier         int            `json:"tier"`
	Tools        []string       `json:"tools"`
	Skills       []string       `json:"skills"`
	Memory       bool           `json:"memory"`
	Thinking     bool           `json:"thinking"`
	Unconfined   bool           `json:"unconfined"`
	Color        string         `json:"color"`
	Options      map[string]any `json:"options"`
}

func (b *agentBody) into(a *agent.Agent) {
	a.Name = b.Name
	a.Description = b.Description
	a.Model = b.Model
	a.Instructions = b.Instructions
	a.Tier = b.Tier
	a.Tools = b.Tools
	a.Skills = b.Skills
	a.Memory = b.Memory
	a.Thinking = b.Thinking
	a.Unconfined = b.Unconfined
	a.Color = b.Color
	a.Options = b.Options
	// 規定エージェントの Tier は変えられない。入口が 2 つある状態にも、
	// 入口が 1 つも無い状態にもしないため。
	if a.ID == agent.DefaultID {
		a.Tier = 0
	}
}

// handleCreateAgent は新しい定義を作る。保存先は探索パスの先頭。
func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var b agentBody
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := agent.ValidateID(b.ID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, ok := s.agents.Get(b.ID); ok {
		writeError(w, http.StatusConflict,
			&agent.FieldError{Field: "id", Reason: fmt.Sprintf("ID %q は既に使われています", b.ID)})
		return
	}
	if b.ID == agent.DefaultID {
		// 規定エージェントは常に存在するため、作成として来ることはない。
		writeError(w, http.StatusConflict,
			&agent.FieldError{Field: "id", Reason: "この ID は規定エージェントが使っています"})
		return
	}
	dir := s.agentDir()
	if dir == "" {
		writeError(w, http.StatusBadRequest, errors.New("エージェントの保存先が設定されていません"))
		return
	}

	a := &agent.Agent{ID: b.ID}
	b.into(a)
	if err := agent.Save(dir, a); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.reloadAgents()
	s.writeAgent(w, b.ID)
}

// handleUpdateAgent は既存の定義を読み込み元のファイルへ書き戻す。
func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cur, ok := s.agents.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("エージェント %q が見つかりません", id))
		return
	}
	var b agentBody
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// ID はファイル名から導出するため変えられない。名前を変えたいときは
	// 複製して古い方を消す。
	next := *cur
	b.into(&next)
	if err := agent.Save(s.agentDir(), &next); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.reloadAgents()
	s.writeAgent(w, id)
}

// handleDeleteAgent は定義ファイルを消す。使っているセッションは残す。
// v1 と同じく「開けるが続行できない」扱いになる。
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, ok := s.agents.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("エージェント %q が見つかりません", id))
		return
	}
	if err := agent.Delete(a); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	// 既定が消えたままだと新しい会話を作れない。入口へ戻す。
	if s.cfg.DefaultAgent == id {
		s.cfg.DefaultAgent = agent.DefaultID
		if err := s.cfg.Save(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.reloadAgents()
	w.WriteHeader(http.StatusNoContent)
}

// agentDir は新しい定義の保存先。先に書いた探索パスが優先される規則に合わせ、
// 書き出しも先頭へ行う。
func (s *Server) agentDir() string {
	if len(s.cfg.AgentPaths) == 0 {
		return ""
	}
	return s.cfg.AgentPaths[0]
}

// reloadAgents は保存した内容を読み直す。書いたものを自分で読み返すことで、
// 画面が返す一覧とファイルの中身が食い違わない。
func (s *Server) reloadAgents() {
	agent.Bootstrap(s.cfg.AgentPaths)
	s.agents.Load(s.cfg.AgentPaths)
}

func (s *Server) writeAgent(w http.ResponseWriter, id string) {
	a, ok := s.agents.Get(id)
	if !ok {
		writeError(w, http.StatusInternalServerError,
			fmt.Errorf("保存した定義 %q を読み直せませんでした", id))
		return
	}
	writeJSON(w, http.StatusOK, a)
}
