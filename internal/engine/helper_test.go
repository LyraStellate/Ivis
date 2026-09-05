package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
)

// mockProvider は実行ループの検証用。提供元の抽象を設けた目的がこれである。
type mockProvider struct {
	// script は n 回目の呼び出しで流すイベント列を返す。
	script func(n int, req provider.Request) []provider.Event
	calls  int
	reqs   []provider.Request
	ctxLen int
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Chat(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	m.reqs = append(m.reqs, req)
	n := m.calls
	m.calls++

	ch := make(chan provider.Event, 8)
	for _, ev := range m.script(n, req) {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Models(ctx context.Context) ([]provider.Model, error) { return nil, nil }
func (m *mockProvider) Health(ctx context.Context) error                     { return nil }

// ctxLen は文脈長として返す値。0 なら「分からない」を表す。
func (m *mockProvider) ContextLength(ctx context.Context, model string) (int, error) {
	return m.ctxLen, nil
}

func text(s string) provider.Event {
	return provider.Event{Type: provider.EventDelta, Text: s}
}

func callTool(name string, args map[string]any) provider.Event {
	return provider.Event{Type: provider.EventToolCalls, ToolCalls: []provider.ToolCall{
		{ID: "c1", Name: name, Arguments: args},
	}}
}

type fixture struct {
	eng    *Engine
	mock   *mockProvider
	store  *store.Store
	cfg    *config.Config
	events []Event
}

func (f *fixture) emit(ev Event) { f.events = append(f.events, ev) }

func (f *fixture) typesOf() []string {
	out := make([]string, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

// writeAgent はエージェント定義をファイルとして置く。定義はファイルが正なので、
// テストでも同じ経路で読ませる。
func writeAgent(t *testing.T, dir, id string, a map[string]any) {
	t.Helper()
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T, script func(n int, req provider.Request) []provider.Event) *fixture {
	t.Helper()

	root := t.TempDir()
	agentsDir := filepath.Join(root, "agents")
	skillsDir := filepath.Join(root, "skills")
	work := filepath.Join(root, "workspace")
	for _, d := range []string{agentsDir, skillsDir, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeAgent(t, agentsDir, "main", map[string]any{
		"name":         "Main",
		"model":        "mock-model",
		"instructions": "テスト用",
		"tier":         1,
		"tools":        []string{"list_dir", "read_file", "write_file", "delegate", "ask_user"},
		"skills":       []string{"*"},
	})
	// child は main より下位。委譲もできるので、上位や同位を呼べないことの
	// 確認にも使う。
	writeAgent(t, agentsDir, "child", map[string]any{
		"name":         "Child",
		"model":        "mock-model",
		"instructions": "子",
		"tier":         2,
		"tools":        []string{"list_dir", "delegate"},
		"skills":       []string{},
	})
	// peer は child と同じ Tier。同位どうしが呼べないことの確認に使う。
	writeAgent(t, agentsDir, "peer", map[string]any{
		"name":         "Peer",
		"model":        "mock-model",
		"instructions": "同位",
		"tier":         2,
		"tools":        []string{"list_dir"},
		"skills":       []string{},
	})
	// remember は記憶を有効にした子。引き継ぎの確認に使う。
	writeAgent(t, agentsDir, "remember", map[string]any{
		"name":         "Remember",
		"model":        "mock-model",
		"instructions": "覚える",
		"tier":         2,
		"memory":       true,
		"tools":        []string{"list_dir"},
		"skills":       []string{},
	})

	st, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	agents := agent.NewSet()
	agents.Load([]string{agentsDir})
	if len(agents.List()) != 4 {
		t.Fatalf("エージェントが読めていません: %v", agents.Errors())
	}
	skills := skillreg.New()
	skills.Load([]string{skillsDir})

	cfg := config.Default()
	cfg.WorkspaceDir = work
	cfg.MaxIterations = 4
	cfg.MaxDelegationDepth = 2
	cfg.ScriptTimeoutSec = 5
	cfg.RequireApproval = false

	mock := &mockProvider{script: script}
	f := &fixture{mock: mock, store: st, cfg: cfg}
	f.eng = &Engine{
		Cfg:      cfg,
		Store:    st,
		Agents:   agents,
		Skills:   skills,
		Tools:    tools.NewRegistry(),
		Provider: mock,
	}
	return f
}

func (f *fixture) newSession(t *testing.T, agentID string) string {
	t.Helper()
	s, err := f.store.CreateSession(context.Background(), agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}
