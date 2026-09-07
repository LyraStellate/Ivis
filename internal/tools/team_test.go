package tools

import (
	"context"
	"strings"
	"testing"
)

// teamEC は boss (窓口) / hand / scout の 3 人で、いま hand が手番を取って
// いる状態を作る。
func teamEC(self string) (*ExecContext, *[]TeamMessage) {
	var sent []TeamMessage
	tc := &TeamContext{
		Self:       self,
		LeadID:     "boss",
		Anyone:     "*",
		AcceptWord: "受諾",
		RejectWord: "却下",
		Members: []TeamMember{
			{ID: "boss", Name: "Boss", Relation: "報告"},
			{ID: "hand", Name: "Hand", Relation: "依頼"},
			{ID: "scout", Name: "Scout", Relation: "依頼"},
		},
	}
	ec := &ExecContext{AgentID: self, Team: tc}
	tc.Send = func(ctx context.Context, msg TeamMessage) error {
		sent = append(sent, msg)
		return nil
	}
	return ec, &sent
}

func args(to string, extra ...string) map[string]any {
	a := map[string]any{"to": to, "why": "頼まれた", "did": "調べた", "message": "できました"}
	for i := 0; i+1 < len(extra); i += 2 {
		a[extra[i]] = extra[i+1]
	}
	return a
}

func send(t *testing.T, ec *ExecContext, a map[string]any) (string, error) {
	t.Helper()
	return (&sendMessageTool{}).Execute(context.Background(), ec, a)
}

func TestSendMessageDelivers(t *testing.T) {
	ec, sent := teamEC("hand")
	if _, err := send(t, ec, args("scout")); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 {
		t.Fatalf("送られた数 = %d", len(*sent))
	}
	m := (*sent)[0]
	if m.To != "scout" || m.Why != "頼まれた" || m.Did != "調べた" || m.Body != "できました" {
		t.Errorf("内訳が落ちている: %+v", m)
	}
}

// 宛先に @ を付けて書くモデルは居る。剥がして受ける。
func TestSendMessageStripsAtSign(t *testing.T) {
	ec, sent := teamEC("hand")
	if _, err := send(t, ec, args("@scout")); err != nil {
		t.Fatal(err)
	}
	if (*sent)[0].To != "scout" {
		t.Errorf("宛先 = %q", (*sent)[0].To)
	}
}

func TestSendMessageRejections(t *testing.T) {
	cases := []struct {
		name string
		self string
		args map[string]any
		want string
	}{
		// 名簿に居ない相手。断る理由に名簿を添えないと、同じ宛先をもう一度試す。
		{"名簿外", "hand", args("居ない"), "居ません"},
		{"自分自身", "hand", args("hand"), "自分自身"},
		// 宛先を決めるのは窓口の仕事。誰でも使えると、決めないまま回り続ける。
		{"窓口以外の *", "hand", args("*"), "窓口"},
		{"本文が空", "hand", args("scout", "message", "  "), "message が空"},
		{"知らない可否", "hand", args("scout", "decision", "たぶん"), "decision"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ec, sent := teamEC(c.self)
			_, err := send(t, ec, c.args)
			if err == nil {
				t.Fatal("断られていない")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("理由 = %q, want に %q を含む", err.Error(), c.want)
			}
			if len(*sent) != 0 {
				t.Error("断ったのに送られている")
			}
		})
	}
}

// 窓口だけは宛先を委ねられる。ただしそれは「決められなかった」なので、
// 次の手番にはならない。
func TestLeadMaySendToAnyone(t *testing.T) {
	ec, sent := teamEC("boss")
	out, err := send(t, ec, args("*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || (*sent)[0].To != "*" {
		t.Errorf("送られていない: %+v", *sent)
	}
	if !strings.Contains(out, "終わります") {
		t.Errorf("枝が終わることが伝わっていない: %q", out)
	}
}

// 同位からの依頼に返すなら、受諾か却下かを必ず言う。言わないと、依頼した側は
// 自分の仕事が進むのかどうか分からないまま待つ。
func TestReplyToRequestNeedsDecision(t *testing.T) {
	ec, sent := teamEC("hand")
	ec.Team.RequesterID = "scout"

	if _, err := send(t, ec, args("scout")); err == nil {
		t.Fatal("可否なしで通ってしまう")
	} else if !strings.Contains(err.Error(), "decision") {
		t.Errorf("理由 = %q", err.Error())
	}
	if len(*sent) != 0 {
		t.Fatal("断ったのに送られている")
	}

	if _, err := send(t, ec, args("scout", "decision", "却下")); err != nil {
		t.Fatalf("却下が通らない: %v", err)
	}
	if (*sent)[0].Decision != "却下" {
		t.Errorf("可否 = %q", (*sent)[0].Decision)
	}

	// 依頼元以外へ送るときは要らない。
	if _, err := send(t, ec, args("boss")); err != nil {
		t.Errorf("依頼元でない相手にも可否を求めている: %v", err)
	}
}

// 直列の会話ではそもそも送れない。
func TestSendMessageNeedsTeam(t *testing.T) {
	if _, err := send(t, &ExecContext{AgentID: "x"}, args("scout")); err == nil {
		t.Error("チームでないのに送れてしまう")
	}
}

// チーム専用のツールは、直列の会話ではモデルへ渡らない。渡すと、モデルは
// 呼べるものとして扱い、呼んでから使えないと返されることになる。
func TestTeamToolsAreHiddenInSeries(t *testing.T) {
	r := NewRegistry()
	all := func(string) bool { return true }

	var series, team []string
	for _, d := range r.Defs(all, false) {
		series = append(series, d.Name)
	}
	for _, d := range r.Defs(all, true) {
		team = append(team, d.Name)
	}
	for _, name := range []string{"send_message", "create_ticket", "update_ticket",
		"get_ticket", "list_tickets"} {
		if contains(series, name) {
			t.Errorf("直列の会話に %s が渡っている", name)
		}
		if !contains(team, name) {
			t.Errorf("チームに %s が渡っていない", name)
		}
	}
	// チーム以外のツールは両方に出る。
	if !contains(series, "read_file") || !contains(team, "read_file") {
		t.Error("ふつうのツールが落ちている")
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
