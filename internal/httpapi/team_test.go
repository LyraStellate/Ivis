package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestRosterJoinAndLeave(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)

	code, body := call(t, s, http.MethodGet, "/api/sessions/"+id+"/agents", "")
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if got := ids(body, "members"); len(got) != 1 || got[0] != "general" {
		t.Errorf("members = %v", got)
	}
	if body["lead_id"] != "general" {
		t.Errorf("lead_id = %v", body["lead_id"])
	}

	// 窓口は外せない。宛先の無い発言の行き先が消える。
	code, body = call(t, s, http.MethodPost, "/api/sessions/"+id+"/members",
		`{"agent_id":"general","join":false}`)
	if code != http.StatusConflict {
		t.Errorf("窓口を外せてしまう: %d %v", code, body)
	}

	// 定義の無い相手は参加させられない。最初の送信ではじめて失敗する形に
	// しないため。
	code, _ = call(t, s, http.MethodPost, "/api/sessions/"+id+"/members",
		`{"agent_id":"nobody","join":true}`)
	if code != http.StatusBadRequest {
		t.Errorf("居ない相手が参加できてしまう: %d", code)
	}
}

func TestSessionAgentLifecycle(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)
	base := "/api/sessions/" + id + "/agents"

	// 共通が使っている ID は、この会話では使えない。宛先が一意に決まらない。
	code, body := call(t, s, http.MethodPost, base, `{"id":"general","model":"m","tier":2}`)
	if code != http.StatusConflict {
		t.Errorf("共通と同じ ID が通ってしまう: %d %v", code, body)
	}

	code, body = call(t, s, http.MethodPost, base,
		`{"id":"reviewer","name":"Reviewer","model":"m","tier":2,"tools":["send_message"]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if got := ids(body, "members"); strings.Join(got, ",") != "general,reviewer" {
		t.Errorf("members = %v", got)
	}
	// 固有の定義はセッションの下に置かれる。共通の一覧には出ない。
	if _, err := os.Stat(filepath.Join(s.cfg.SessionAgentsDir(id), "reviewer.json")); err != nil {
		t.Errorf("定義が置かれていない: %v", err)
	}
	if _, ok := s.agents.Get("reviewer"); ok {
		t.Error("固有の定義が共通の一覧に出ている")
	}

	// 2 度目は断る。
	code, _ = call(t, s, http.MethodPost, base, `{"id":"reviewer","model":"m","tier":2}`)
	if code != http.StatusConflict {
		t.Errorf("同じ ID が 2 度作れてしまう: %d", code)
	}

	// 書き戻し。
	code, body = call(t, s, http.MethodPut, base+"/reviewer",
		`{"id":"reviewer","name":"直した","model":"m","tier":3}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	for _, m := range body["members"].([]any) {
		mm := m.(map[string]any)
		if mm["id"] == "reviewer" && mm["name"] != "直した" {
			t.Errorf("書き戻せていない: %v", mm["name"])
		}
	}

	// 削除。
	code, body = call(t, s, http.MethodDelete, base+"/reviewer", "")
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if got := ids(body, "members"); len(got) != 1 {
		t.Errorf("消えていない: %v", got)
	}
}

// 窓口は消せない。移してからでなければ、その会話は何も受け取れなくなる。
func TestCannotDeleteLead(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)
	code, _ := call(t, s, http.MethodDelete, "/api/sessions/"+id+"/agents/general", "")
	if code != http.StatusConflict {
		t.Errorf("状態 = %d, want 409", code)
	}
}

// 写しは元と別の名前を持ち、元と縁が切れる。
func TestCopyAgentIntoSession(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)

	code, body := call(t, s, http.MethodPost, "/api/sessions/"+id+"/agents/copy",
		`{"agent_id":"general"}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	var copied map[string]any
	for _, m := range body["members"].([]any) {
		mm := m.(map[string]any)
		if mm["local"] == true {
			copied = mm
		}
	}
	if copied == nil {
		t.Fatal("写しが名簿に無い")
	}
	if copied["id"] == "general" {
		t.Error("元と同じ ID で写している")
	}
	if copied["name"] != "General" {
		t.Errorf("中身が写っていない: %v", copied["name"])
	}
	// 規定エージェントの写しは Tier 0 のまま。対等な 2 人組は正当な構成で、
	// 同位どうしは依頼しかできないという規則がそれを支える (#731906)。
	if copied["tier"].(float64) != 0 {
		t.Errorf("Tier = %v, want 0", copied["tier"])
	}
	if copied["fixed"] == true {
		t.Error("写しが規定エージェント扱いになっている")
	}
	// 元は共通のまま残る。
	if _, ok := s.agents.Get("general"); !ok {
		t.Error("元の定義が消えている")
	}
}

// 会話を消すと固有の定義も消える。作業ディレクトリは残す。
func TestDeleteSessionRemovesLocalAgents(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)
	call(t, s, http.MethodPost, "/api/sessions/"+id+"/agents",
		`{"id":"reviewer","model":"m","tier":2}`)

	dir := s.cfg.SessionAgentsDir(id)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("定義の置き場が無い: %v", err)
	}
	code, _ := call(t, s, http.MethodDelete, "/api/sessions/"+id, "")
	if code != http.StatusNoContent {
		t.Fatalf("状態 = %d", code)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("固有の定義が残っている: %v", err)
	}
}

func TestTicketEndpoints(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)
	base := "/api/sessions/" + id + "/tickets"

	code, body := call(t, s, http.MethodPost, base,
		`{"title":"設計の確認","body":"やること","priority":"高"}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if body["number"].(float64) != 1 {
		t.Errorf("番号 = %v, want 1", body["number"])
	}
	if body["status"] != "新規" {
		t.Errorf("状態 = %v", body["status"])
	}

	// 状態を進めると注記が自動で残る。
	code, body = call(t, s, http.MethodPatch, base+"/1", `{"status":"進行中"}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	notes := body["notes"].([]any)
	if len(notes) != 1 || notes[0].(map[string]any)["auto"] != true {
		t.Errorf("自動の注記が残っていない: %v", notes)
	}

	code, body = call(t, s, http.MethodPost, base+"/1/notes", `{"body":"調べた"}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if len(body["notes"].([]any)) != 2 {
		t.Errorf("注記が足せていない: %v", body["notes"])
	}

	// 終了は既定の一覧に出ない。closed=1 なら出る。
	call(t, s, http.MethodPatch, base+"/1", `{"status":"終了"}`)
	code, list := callList(t, s, http.MethodGet, base, "")
	if code != http.StatusOK || len(list) != 0 {
		t.Errorf("終了が既定で出ている: %d %v", code, list)
	}
	_, list = callList(t, s, http.MethodGet, base+"?closed=1", "")
	if len(list) != 1 {
		t.Errorf("終了込みで出ない: %v", list)
	}

	// 知らない状態は断る。
	code, _ = call(t, s, http.MethodPatch, base+"/1", `{"status":"架空"}`)
	if code != http.StatusBadRequest && code != http.StatusInternalServerError {
		t.Errorf("知らない状態が通ってしまう: %d", code)
	}

	code, _ = call(t, s, http.MethodDelete, base+"/1", "")
	if code != http.StatusNoContent {
		t.Errorf("消せない: %d", code)
	}
	code, _ = call(t, s, http.MethodGet, base+"/1", "")
	if code != http.StatusNotFound {
		t.Errorf("消したものが読める: %d", code)
	}
}

// ツールの一覧には、チーム専用かどうかの印が付く。許可はできるが、直列の
// 会話では渡らない。
func TestToolListMarksTeamOnly(t *testing.T) {
	s, _, _ := newServer(t)
	_, list := callList(t, s, http.MethodGet, "/api/tools", "")
	found := false
	for _, v := range list {
		m := v.(map[string]any)
		if m["name"] == "send_message" {
			found = true
			if m["team_only"] != true {
				t.Error("チーム専用の印が付いていない")
			}
		}
		if m["name"] == "read_file" && m["team_only"] == true {
			t.Error("ふつうのツールにチームの印が付いている")
		}
	}
	if !found {
		t.Error("send_message が一覧に無い")
	}
}

// 窓口は名簿の中から選べる。共通の一覧だけを見ていると、その会話のために
// 作った担当を窓口にできず、規定エージェントを外せないままになる (#731906)。
func TestLeadCanMoveToLocalAgentThenGeneralLeaves(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)

	code, body := call(t, s, http.MethodPost, "/api/sessions/"+id+"/agents",
		`{"id":"boss","name":"Boss","model":"m","tier":0}`)
	if code != http.StatusOK {
		t.Fatalf("固有の担当を作れない: %d %v", code, body)
	}

	// 窓口を固有の担当へ移す。
	code, body = call(t, s, http.MethodPatch, "/api/sessions/"+id, `{"agent_id":"boss"}`)
	if code != http.StatusOK {
		t.Fatalf("窓口を移せない: %d %v", code, body)
	}
	if body["agent_id"] != "boss" {
		t.Errorf("agent_id = %v, want boss", body["agent_id"])
	}

	// 移したので general を外せる。
	code, body = call(t, s, http.MethodPost, "/api/sessions/"+id+"/members",
		`{"agent_id":"general","join":false}`)
	if code != http.StatusOK {
		t.Fatalf("general を外せない: %d %v", code, body)
	}
	if got := ids(body, "members"); strings.Join(got, ",") != "boss" {
		t.Errorf("名簿 = %v, want [boss]", got)
	}
	if body["lead_id"] != "boss" {
		t.Errorf("lead_id = %v", body["lead_id"])
	}
}

// 名簿に居ない相手は窓口にできない。宛先の無い発言の行き先が消える。
func TestLeadMustBeAMember(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)
	code, body := call(t, s, http.MethodPatch, "/api/sessions/"+id, `{"agent_id":"nobody"}`)
	if code != http.StatusBadRequest {
		t.Errorf("状態 = %d, want 400 (%v)", code, body)
	}
}

// 直列の会話は今までどおり、共通の一覧から選ぶ。
func TestSeriesAgentStillFromCommonSet(t *testing.T) {
	s, _, _ := newServer(t)
	_, sess := call(t, s, http.MethodPost, "/api/sessions", `{}`)
	id := sess["id"].(string)
	code, _ := call(t, s, http.MethodPatch, "/api/sessions/"+id, `{"agent_id":"nobody"}`)
	if code != http.StatusBadRequest {
		t.Errorf("状態 = %d, want 400", code)
	}
	code, body := call(t, s, http.MethodPatch, "/api/sessions/"+id, `{"agent_id":"general"}`)
	if code != http.StatusOK || body["agent_id"] != "general" {
		t.Errorf("%d %v", code, body)
	}
}
