package tools

import (
	"context"
	"fmt"
	"strings"
)

// TeamMember は名簿の 1 人。engine が team.Roster から詰めて渡す。
//
// Relation には、この手番を取っているメンバーから見て何ができるか (報告 /
// 依頼 / 指示) が入る。ツールが Tier から計算し直さないのは、権限の向きを
// 決める場所が 2 か所あると、いつか食い違うためである。
type TeamMember struct {
	ID       string
	Name     string
	Relation string
}

// TeamMessage は 1 通のメッセージ。
type TeamMessage struct {
	To  string
	Why string
	Did string
	// Body は相手へのメッセージ本文。
	Body string
	// Decision は依頼への返答のときだけ、受諾か却下。
	Decision string
}

// TeamContext はチームセッションの手番 1 回分。直列の会話では nil になり、
// チーム専用のツールはそもそもモデルへ渡らない。
type TeamContext struct {
	// Self はいま手番を取っているメンバー。
	Self string
	// LeadID は窓口。宛先を "*" にできるのはここだけである。
	LeadID  string
	Members []TeamMember
	// RequesterID は、この手番を始めさせた依頼の送り主。依頼でなければ空。
	// ここへ返すときは可否を添えなければならない。
	RequesterID string
	// Anyone は宛先を決めずに送るときの印。engine が team 側の値を渡す。
	Anyone string
	// AcceptWord と RejectWord は可否に使える語。
	AcceptWord string
	RejectWord string
	// Send は 1 通送る。保存と待ち行列への積み込みは engine が行う。
	Send func(ctx context.Context, msg TeamMessage) error
}

// member は名簿から 1 人引く。
func (tc *TeamContext) member(id string) (TeamMember, bool) {
	for _, m := range tc.Members {
		if m.ID == id {
			return m, true
		}
	}
	return TeamMember{}, false
}

// roll は断るときに添える名簿。誰に送れるのか分からないまま断られると、
// モデルは同じ宛先をもう一度試す。
func (tc *TeamContext) roll() string {
	var names []string
	for _, m := range tc.Members {
		if m.ID == tc.Self {
			continue
		}
		names = append(names, m.ID)
	}
	if len(names) == 0 {
		return "この会話にはほかのメンバーが居ません。"
	}
	return "送れる相手: " + strings.Join(names, ", ")
}

// teamOnly は「チームセッションでしか意味を持たない」ことを示す。
//
// Tool の作法として別の口を設けず、この 1 つの真偽で表す。ツール名の表を
// どこかに置く形にすると、ツールを増やしたときにその表を直し忘れる。
type teamOnly struct{}

func (teamOnly) TeamOnly() bool { return true }

// teamScoped は teamOnly を持つツールを見分けるための口。ツール全部に
// メソッドを足さずに済ませるため、実装している側だけを型で判別する。
type teamScoped interface{ TeamOnly() bool }

// IsTeamOnly はそのツールがチーム専用かを返す。
func IsTeamOnly(t Tool) bool {
	s, ok := t.(teamScoped)
	return ok && s.TeamOnly()
}

// changer は、実行すると画面が持っている一覧が古くなるツール。何が古く
// なったのかを名前で返す。
//
// ツール名の表をどこか別の場所に置かないのは、ツールを増やしたときにその表を
// 直し忘れるからである。チーム専用かどうかを見分けるのと同じ考え方で、
// 知っているもの自身に答えさせる。
type changer interface{ Changes() string }

// Changes はそのツールが古くするものの名前を返す。何も古くしなければ空。
func Changes(t Tool) string {
	c, ok := t.(changer)
	if !ok {
		return ""
	}
	return c.Changes()
}

type sendMessageTool struct{ teamOnly }

func (t *sendMessageTool) Name() string { return "send_message" }
func (t *sendMessageTool) Description() string {
	return "チームの別のメンバーへメッセージを送る。相手はこの会話のやり取りを見られないので、" +
		"これ 1 通で読めるように書くこと。上位へは報告、同位へは依頼 (受諾か却下が返る)、下位へは指示になる。"
}
func (t *sendMessageTool) Parameters() map[string]any {
	return schema(map[string]any{
		"to":       strProp("宛先のメンバー ID。窓口だけは \"*\" を指定して、宛先の判断を委ねられる。"),
		"why":      strProp("なぜそれをすることになったか。誰にどう頼まれたのかを書く。"),
		"did":      strProp("この手番で実際にやったこと。まだ何もしていないなら、これから何をするか。"),
		"message":  strProp("相手へのメッセージ本文。相手が受け取るのはこれと why と did だけである。"),
		"decision": strProp("同位からの依頼に返すときだけ、受諾 か 却下 のいずれか。却下の理由は message に書く。"),
	}, "to", "why", "did", "message")
}

// 送ること自体は会話の外へ出ない。相手が使うツールは相手の手番で承認を求める。
func (t *sendMessageTool) NeedsApproval() bool { return false }

func (t *sendMessageTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	tc := ec.Team
	if tc == nil || tc.Send == nil {
		return "", fmt.Errorf("この会話ではメッセージを送れません")
	}
	to, err := argString(args, "to")
	if err != nil {
		return "", err
	}
	to = strings.TrimPrefix(strings.TrimSpace(to), "@")

	msg := TeamMessage{
		To:       to,
		Why:      strings.TrimSpace(argStringOpt(args, "why")),
		Did:      strings.TrimSpace(argStringOpt(args, "did")),
		Body:     strings.TrimSpace(argStringOpt(args, "message")),
		Decision: strings.TrimSpace(argStringOpt(args, "decision")),
	}
	if msg.Body == "" {
		return "", fmt.Errorf("message が空です。相手が受け取るのはこの本文なので、空では何も伝わりません")
	}

	if to == tc.Self {
		return "", fmt.Errorf("自分自身へは送れません。%s", tc.roll())
	}
	if to == tc.Anyone {
		// 宛先を選ぶのは窓口の仕事である。誰でも "*" を使えると、決めない
		// まま回し続けることになる。
		if tc.Self != tc.LeadID {
			return "", fmt.Errorf("宛先に %q を指定できるのは窓口 (%s) だけです。相手を決めてください。%s",
				tc.Anyone, tc.LeadID, tc.roll())
		}
	} else if _, ok := tc.member(to); !ok {
		return "", fmt.Errorf("%q はこの会話に居ません。%s", to, tc.roll())
	}

	// 同位からの依頼に返すなら、受諾か却下かを必ず言う。言わないと、依頼した
	// 側は自分の仕事が進むのかどうか分からないまま待つ。
	if tc.RequesterID != "" && to == tc.RequesterID {
		switch msg.Decision {
		case tc.AcceptWord, tc.RejectWord:
		default:
			return "", fmt.Errorf("%s からの依頼への返答なので、decision に %q か %q を指定してください",
				tc.RequesterID, tc.AcceptWord, tc.RejectWord)
		}
	} else if msg.Decision != "" && msg.Decision != tc.AcceptWord && msg.Decision != tc.RejectWord {
		return "", fmt.Errorf("decision に指定できるのは %q か %q だけです", tc.AcceptWord, tc.RejectWord)
	}

	if err := tc.Send(ctx, msg); err != nil {
		return "", err
	}
	if to == tc.Anyone {
		return "宛先を決められなかったものとして記録しました。この枝はここで終わります。", nil
	}
	return fmt.Sprintf("%s へ送りました。返事は次の手番で届きます。この手番で伝えることが"+
		"ほかに無ければ、ツールを呼ばずに終えてください。", to), nil
}
