package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
)

// stubProvider は生成が呼ばれたかどうかだけを見る。
type stubProvider struct{ calls int }

func (p *stubProvider) Name() string { return "stub" }
func (p *stubProvider) Chat(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	p.calls++
	ch := make(chan provider.Event, 2)
	ch <- provider.Event{Type: provider.EventDelta, Text: "はい"}
	ch <- provider.Event{Type: provider.EventDone}
	close(ch)
	return ch, nil
}
func (p *stubProvider) Models(ctx context.Context) ([]provider.Model, error) { return nil, nil }
func (p *stubProvider) Health(ctx context.Context) error                     { return nil }
func (p *stubProvider) ContextLength(ctx context.Context, model string) (int, error) {
	return 8192, nil
}

func newServer(t *testing.T) (*Server, *stubProvider, *store.Store) {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	def := `{"name":"General","model":"m","instructions":"テスト","tier":0,"tools":[],"skills":[]}`
	if err := os.WriteFile(filepath.Join(agentsDir, "general.json"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := config.Default()
	cfg.DataDir = root
	cfg.WorkspaceDir = filepath.Join(root, "workspace")
	cfg.AgentPaths = []string{agentsDir}
	cfg.Discord.Enabled = false

	agents := agent.NewSet()
	agents.Load(cfg.AgentPaths)
	teamAgents := agent.NewSet()
	teamAgents.LoadTeam(cfg.TeamAgentPaths())

	prov := &stubProvider{}
	s := New(Deps{
		Config: cfg, Store: st, Agents: agents, TeamAgents: teamAgents,
		Skills: skillreg.New(),
		Tools:  tools.NewRegistry(), Prov: prov,
		NewProvider: func(string) provider.Provider { return prov },
		Log:         func(string, ...any) {},
	})
	t.Cleanup(s.Close)
	return s, prov, st
}

// send は 1 件送り、流れてきたイベントの型と本文を返す。
func send(t *testing.T, s *Server, sessionID, text string) []map[string]any {
	t.Helper()
	body := strings.NewReader(`{"text":` + quote(text) + `}`)
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/messages", body)
	r.SetPathValue("id", sessionID)
	w := httptest.NewRecorder()
	s.handleSend(w, r)

	var out []map[string]any
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line[6:]), &ev); err != nil {
			t.Fatalf("イベントを読めません: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// コマンドはエージェントへ流さない。流してから判断させると、止めたいという
// 依頼が生成の順番待ちに並ぶ (#486237)。
func TestWebCommandDoesNotReachTheAgent(t *testing.T) {
	s, prov, st := newServer(t)
	sess, err := st.CreateSession(context.Background(), "general", "会話")
	if err != nil {
		t.Fatal(err)
	}

	evs := send(t, s, sess.ID, "/help")
	if prov.calls != 0 {
		t.Fatalf("生成が %d 回走っている", prov.calls)
	}
	if len(evs) == 0 || evs[0]["type"] != "notice" {
		t.Fatalf("返ってきたイベント: %v", evs)
	}
	if !strings.Contains(evs[0]["text"].(string), "/compact") {
		t.Errorf("一覧が返っていない: %v", evs[0]["text"])
	}

	// 履歴には残さない。残すと、それが次の圧縮の材料になり、要約に
	// 「利用者は /help と言った」という無意味な行が残る。
	msgs, err := st.ListMessages(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("履歴が %d 件。コマンドは発言ではない", len(msgs))
	}
}

// 先頭に / が無ければ、それはエージェントへの依頼である。
func TestWebPlainTextStillReachesTheAgent(t *testing.T) {
	s, prov, st := newServer(t)
	sess, err := st.CreateSession(context.Background(), "general", "会話")
	if err != nil {
		t.Fatal(err)
	}

	send(t, s, sess.ID, "こんにちは")
	if prov.calls != 1 {
		t.Fatalf("生成が %d 回", prov.calls)
	}
}

// 履歴を消す・まとめるコマンドは使用量を変える。伝えないと、既に空いて
// いるのに満杯のままの割合が出続ける。
func TestWebCommandReportsTheUsage(t *testing.T) {
	s, _, st := newServer(t)
	ctx := context.Background()
	sess, err := st.CreateSession(ctx, "general", "会話")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(ctx, &store.Message{
		SessionID: sess.ID, Role: provider.RoleUser, Content: "ひとつめ", AgentID: "general",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetContextUsage(ctx, sess.ID, 7000, 8192); err != nil {
		t.Fatal(err)
	}

	evs := send(t, s, sess.ID, "/clear")
	var usage map[string]any
	for _, ev := range evs {
		if ev["type"] == "usage" {
			usage = ev
		}
	}
	if usage == nil {
		t.Fatal("使用量が流れていない。満杯のままの割合が出続ける")
	}
	if usage["prompt_tokens"] != nil && usage["prompt_tokens"].(float64) != 0 {
		t.Fatalf("使用量が %v のまま", usage["prompt_tokens"])
	}
}
