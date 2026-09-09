package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// チームエージェントは全てのチーム会話で共有される (#731906)。
//
// 会話ごとに違うのは「どれを有効にしてあるか」だけで、定義は 1 つきりである。
// だから編集と削除の経路は会話 ID の下に無い。

// 有効化と解除。定義そのものには触れない。
func TestEnableAndDisable(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)

	code, body := call(t, s, http.MethodGet, "/api/sessions/"+id+"/agents", "")
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if got := ids(body, "members"); len(got) != 1 || got[0] != "general" {
		t.Errorf("members = %v", got)
	}
	// 窓口はもう無い。名簿の応答にも出てはいけない。
	if _, ok := body["lead_id"]; ok {
		t.Error("窓口が応答に残っている")
	}

	// 誰でも外せる。名簿が空になることも許す — 空の名簿は送るときに
	// 「メンバーが 1 人も居ません」と言い、直す場所は目の前にある。
	code, body = call(t, s, http.MethodPost, "/api/sessions/"+id+"/members",
		`{"agent_id":"general","join":false}`)
	if code != http.StatusOK {
		t.Fatalf("外せない: %d %v", code, body)
	}
	if got := ids(body, "members"); len(got) != 0 {
		t.Errorf("外れていない: %v", got)
	}
	// 外れたものは「使えるもの」の側へ回る。
	if got := ids(body, "available"); len(got) != 1 || got[0] != "general" {
		t.Errorf("available = %v", got)
	}

	// 定義の無い相手は有効にできない。最初の送信ではじめて失敗する形に
	// しないため。
	code, _ = call(t, s, http.MethodPost, "/api/sessions/"+id+"/members",
		`{"agent_id":"nobody","join":true}`)
	if code != http.StatusBadRequest {
		t.Errorf("居ない相手を有効にできてしまう: %d", code)
	}
}

// 作った定義が、別の会話からも有効にできること。
func TestTeamAgentIsSharedAcrossSessions(t *testing.T) {
	s, _, _ := newServer(t)
	a := newTeam(t, s)
	b := newTeam(t, s)

	code, body := call(t, s, http.MethodPost, "/api/sessions/"+a+"/agents",
		`{"id":"reviewer","name":"Reviewer","model":"m","tier":2,"tools":["send_message"]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	// 作った会話では、そのまま有効になる。作ってから「有効化」をもう一度
	// 押させる形にはしない。
	if got := ids(body, "members"); strings.Join(got, ",") != "general,reviewer" {
		t.Errorf("作った会話の名簿 = %v", got)
	}
	// 定義は共有の置き場にある。共通の一覧には出ない。
	if _, err := os.Stat(filepath.Join(s.cfg.TeamAgentDir(), "reviewer.json")); err != nil {
		t.Errorf("定義が置かれていない: %v", err)
	}
	if _, ok := s.agents.Get("reviewer"); ok {
		t.Error("チームエージェントが共通の一覧に出ている")
	}

	// もう一方の会話からは、まだ有効ではないが選べる。
	code, body = call(t, s, http.MethodGet, "/api/sessions/"+b+"/agents", "")
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if has(ids(body, "members"), "reviewer") {
		t.Error("別の会話で勝手に有効になっている")
	}
	if !has(ids(body, "available"), "reviewer") {
		t.Errorf("別の会話から選べない: %v", ids(body, "available"))
	}

	// 有効にできる。
	code, body = call(t, s, http.MethodPost, "/api/sessions/"+b+"/members",
		`{"agent_id":"reviewer","join":true}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if !has(ids(body, "members"), "reviewer") {
		t.Errorf("有効にできない: %v", ids(body, "members"))
	}

	// 由来の印が付いていること。画面はこれで欄を分ける。
	for _, m := range body["members"].([]any) {
		mm := m.(map[string]any)
		want := "common"
		if mm["id"] == "reviewer" {
			want = "team"
		}
		if mm["scope"] != want {
			t.Errorf("%v の由来 = %v, want %v", mm["id"], mm["scope"], want)
		}
	}
}

// 定義を直すと、有効にしている全ての会話に効く。
func TestTeamAgentEditAffectsEverySession(t *testing.T) {
	s, _, _ := newServer(t)
	a := newTeam(t, s)
	b := newTeam(t, s)
	call(t, s, http.MethodPost, "/api/sessions/"+a+"/agents",
		`{"id":"reviewer","name":"Reviewer","model":"m","tier":2}`)
	call(t, s, http.MethodPost, "/api/sessions/"+b+"/members",
		`{"agent_id":"reviewer","join":true}`)

	code, body := call(t, s, http.MethodPut, "/api/team-agents/reviewer",
		`{"id":"reviewer","name":"直した","model":"m","tier":3}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if body["name"] != "直した" {
		t.Errorf("書き戻せていない: %v", body["name"])
	}

	for _, id := range []string{a, b} {
		_, roster := call(t, s, http.MethodGet, "/api/sessions/"+id+"/agents", "")
		for _, m := range roster["members"].([]any) {
			mm := m.(map[string]any)
			if mm["id"] == "reviewer" && mm["name"] != "直した" {
				t.Errorf("%s に効いていない: %v", id, mm["name"])
			}
		}
	}
}

// 消すと、有効にしている全ての会話から居なくなる。名簿からは掃除せず、
// 何が消えたのかを名簿の失敗として出す。
func TestDeleteTeamAgentLeavesATrace(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)
	call(t, s, http.MethodPost, "/api/sessions/"+id+"/agents",
		`{"id":"reviewer","model":"m","tier":2}`)

	code, _ := call(t, s, http.MethodDelete, "/api/team-agents/reviewer", "")
	if code != http.StatusNoContent {
		t.Fatalf("消せない: %d", code)
	}
	_, body := call(t, s, http.MethodGet, "/api/sessions/"+id+"/agents", "")
	if has(ids(body, "members"), "reviewer") {
		t.Error("消えた定義が名簿に残っている")
	}
	errs, _ := body["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("何が消えたのかが出ていない: %v", body["errors"])
	}
	if !strings.Contains(errs[0].(map[string]any)["reason"].(string), "reviewer") {
		t.Errorf("理由 = %v", errs[0])
	}
}

// ID は共通とチームの全体で一意。宛先であると同時に、共有ディレクトリの
// 中のファイル名でもある。
func TestAgentIDIsUniqueEverywhere(t *testing.T) {
	s, _, _ := newServer(t)
	a := newTeam(t, s)
	b := newTeam(t, s)

	code, _ := call(t, s, http.MethodPost, "/api/sessions/"+a+"/agents",
		`{"id":"general","model":"m","tier":2}`)
	if code != http.StatusConflict {
		t.Errorf("共通と同じ ID が通ってしまう: %d", code)
	}

	call(t, s, http.MethodPost, "/api/sessions/"+a+"/agents",
		`{"id":"reviewer","model":"m","tier":2}`)
	// 別の会話からでも、同じ名前はもう使えない。置き場が 1 つになった以上、
	// ファイル名として重ねられない。
	code, _ = call(t, s, http.MethodPost, "/api/sessions/"+b+"/agents",
		`{"id":"reviewer","model":"m","tier":2}`)
	if code != http.StatusConflict {
		t.Errorf("別の会話から同じ ID を作れてしまう: %d", code)
	}
}

// 会話を消しても、共有の定義は消さない。
func TestDeleteSessionKeepsTeamAgents(t *testing.T) {
	s, _, _ := newServer(t)
	a := newTeam(t, s)
	b := newTeam(t, s)
	call(t, s, http.MethodPost, "/api/sessions/"+a+"/agents",
		`{"id":"reviewer","model":"m","tier":2}`)
	call(t, s, http.MethodPost, "/api/sessions/"+b+"/members",
		`{"agent_id":"reviewer","join":true}`)

	if code, _ := call(t, s, http.MethodDelete, "/api/sessions/"+a, ""); code != http.StatusNoContent {
		t.Fatalf("会話を消せない: %d", code)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.TeamAgentDir(), "reviewer.json")); err != nil {
		t.Errorf("共有の定義まで消えている: %v", err)
	}
	_, body := call(t, s, http.MethodGet, "/api/sessions/"+b+"/agents", "")
	if !has(ids(body, "members"), "reviewer") {
		t.Errorf("残った会話から居なくなっている: %v", ids(body, "members"))
	}
}

// 写しは元と別の名前を持ち、元と縁が切れる。
func TestCopyAgentIntoTeam(t *testing.T) {
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
		if mm["scope"] == "team" {
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
	// 規定エージェントの写しは Tier 0 のまま。対等な 2 人組は正当な構成である。
	if copied["tier"].(float64) != 0 {
		t.Errorf("Tier = %v, want 0", copied["tier"])
	}
	if copied["fixed"] == true {
		t.Error("写しが規定エージェント扱いになっている")
	}
	if _, ok := s.agents.Get("general"); !ok {
		t.Error("元の定義が消えている")
	}
}

// チームでは答え手を選ばない。宛先はメンションで決まるので、選ばせる欄が
// あること自体が誤りになる。
func TestTeamRejectsAgentPatch(t *testing.T) {
	s, _, _ := newServer(t)
	id := newTeam(t, s)
	code, body := call(t, s, http.MethodPatch, "/api/sessions/"+id, `{"agent_id":"general"}`)
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

func has(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
