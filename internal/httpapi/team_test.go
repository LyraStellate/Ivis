package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
)

// call は経路を通して 1 件叩き、状態と本文を返す。mux を通すのは、経路の
// 組み方そのものも試験の対象だからである。
func call(t *testing.T, s *Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func callList(t *testing.T, s *Server, method, path, body string) (int, []any) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	var out []any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func newTeam(t *testing.T, s *Server) string {
	t.Helper()
	code, body := call(t, s, http.MethodPost, "/api/sessions",
		`{"agent_id":"general","kind":"team"}`)
	if code != http.StatusOK {
		t.Fatalf("チームを作れない: %d %v", code, body)
	}
	return body["id"].(string)
}

func ids(body map[string]any, key string) []string {
	var out []string
	for _, v := range body[key].([]any) {
		out = append(out, v.(map[string]any)["id"].(string))
	}
	return out
}

func TestCreateTeamSession(t *testing.T) {
	s, _, _ := newServer(t)
	code, body := call(t, s, http.MethodPost, "/api/sessions",
		`{"agent_id":"general","kind":"team"}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if body["kind"] != "team" {
		t.Errorf("kind = %v", body["kind"])
	}
	// 作業ディレクトリはチームの段の下。直列と混ざらない。
	if ws, _ := body["workspace"].(string); !strings.Contains(ws, string(filepath.Separator)+"team"+string(filepath.Separator)) {
		t.Errorf("workspace = %v", ws)
	}
	// 空のチームから始めない。
	if got := body["members"].([]any); len(got) != 1 || got[0] != "general" {
		t.Errorf("members = %v", got)
	}
}

// 種類を書かなければ直列。既存の呼び出しは何も変わらない。
func TestCreateSeriesByDefault(t *testing.T) {
	s, _, _ := newServer(t)
	code, body := call(t, s, http.MethodPost, "/api/sessions", `{}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if body["kind"] != "series" {
		t.Errorf("kind = %v", body["kind"])
	}
	if _, ok := body["members"]; ok {
		t.Errorf("直列の会話が名簿を持っている: %v", body["members"])
	}
}

// 直列の会話にチームの操作を求めるのは、呼ぶ側の誤りである。障害と同じ
// 扱いにすると、画面は直せる誤りと直せない故障を区別できない。
func TestRosterOnSeriesIsClientError(t *testing.T) {
	s, _, _ := newServer(t)
	_, sess := call(t, s, http.MethodPost, "/api/sessions", `{}`)
	code, _ := call(t, s, http.MethodGet, "/api/sessions/"+sess["id"].(string)+"/agents", "")
	if code != http.StatusBadRequest {
		t.Errorf("状態 = %d, want 400", code)
	}
}

// 流れ図は名簿の箱と、記録された矢印を返す (#512740)。
func TestFlowReturnsNodesAndArrows(t *testing.T) {
	s, _, st := newServer(t)
	id := newTeam(t, s)

	// 利用者から general へ 1 通。まだ応えは無い。
	user := &store.Message{SessionID: id, Role: provider.RoleUser,
		Content: "認証を作って", ToAgentID: "general"}
	if err := st.AppendMessage(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	code, body := call(t, s, http.MethodGet, "/api/sessions/"+id+"/flow", "")
	if code != http.StatusOK {
		t.Fatalf("状態 = %d %v", code, body)
	}
	// 箱は利用者と名簿の分。矢印が何本増えても、この数は変わらない。
	if got := ids(body, "nodes"); len(got) != 2 || got[1] != "general" {
		t.Errorf("節 = %v", got)
	}
	arrows := body["arrows"].([]any)
	if len(arrows) != 1 {
		t.Fatalf("矢印 = %v", arrows)
	}
	// 利用者からの矢印は未完了に数えない。名簿の外へは返せない。
	if arrows[0].(map[string]any)["open"] == true {
		t.Error("利用者からの矢印が返事待ちになっている")
	}
}

// 直列の会話に流れ図は無い。
func TestFlowRefusesSeriesSession(t *testing.T) {
	s, _, _ := newServer(t)
	code, body := call(t, s, http.MethodPost, "/api/sessions", `{"agent_id":"general"}`)
	if code != http.StatusOK {
		t.Fatal(body)
	}
	code, _ = call(t, s, http.MethodGet, "/api/sessions/"+body["id"].(string)+"/flow", "")
	if code == http.StatusOK {
		t.Error("直列の会話で流れ図が返っている")
	}
}
