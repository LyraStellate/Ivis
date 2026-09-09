// Package team はチームセッションの名簿を組み立てる。
//
// エージェントの定義は 2 か所にある。共通エージェント (config.AgentPaths) は
// すべての会話で使え、チームエージェント (その下の team/) はチームセッション
// でだけ使える。どちらも定義は 1 つきりで、**全ての会話が同じものを見る**。
//
// 会話ごとに違うのは「どれを有効にしてあるか」だけで、それは Session.Members
// が持つ。名簿はそこに載っている ID を 2 つの一覧から引いて組み立てる。
//
// 名簿の中では由来を区別しない。誰に何を頼めるかは Tier だけで決まり、定義が
// どこにあるかは関係しない。由来 (Scope) を持つのは、画面が欄を分けるため
// だけである (#731906)。
package team

import (
	"fmt"
	"strings"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/store"
)

// メンバーどうしの関係。宛先を決めれば、種別はこの 3 つのどれかに定まる。
// 送り手には選ばせない。選べるようにすると「同位に指示する」が書けてしまい、
// Tier が意味を失う (#640275)。
const (
	// RelReport は上位への報告。受け手に応答の義務はない。
	RelReport = "報告"
	// RelRequest は同位への依頼。受け手は受諾か却下を返す。
	RelRequest = "依頼"
	// RelOrder は下位への指示。受け手は断れない。
	RelOrder = "指示"
)

// 依頼への返答。
const (
	DecisionAccept = "受諾"
	DecisionReject = "却下"
)

// ParseMention は本文の先頭から宛先を取り出し、残りを本文として返す。
//
// 宛先の記述は本文から取り除く。残すと、記録の側は毎回宛先で始まる読みにくい
// 並びになり、モデルへ渡すときにも同じ情報が 2 か所に載る (#640275)。
func ParseMention(text string) (to, body string) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "@") {
		return "", t
	}
	rest := t[1:]
	end := len(rest)
	for i, r := range rest {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '、' || r == '。' {
			end = i
			break
		}
	}
	to = rest[:end]
	if to == "" {
		return "", t
	}
	return to, strings.TrimSpace(rest[end:])
}

// エージェントの由来。画面が欄を分けるために持つ。
const (
	// ScopeCommon は共通エージェント。すべての会話で使える。
	ScopeCommon = "common"
	// ScopeTeam はチームエージェント。チームセッションでだけ使える。
	ScopeTeam = "team"
)

// Member は名簿の 1 人。
type Member struct {
	*agent.Agent
	// Scope は定義がどちらの一覧から来たか。
	Scope string `json:"scope"`
}

// Roster は 1 つのチームセッションの名簿。
type Roster struct {
	// Members は Tier 順、同じ Tier では ID 順。上位から下位へ読める並びは、
	// そのまま指示の流れの向きである。
	Members []*Member
	// Errors は読めなかった定義。1 体欠けても残りは名簿に載る。
	Errors []agent.LoadError
}

// Load は会話の名簿を組み立てる。
//
// 有効にしてある ID を、共通 → チームの順に引く。ファイルは読まない —
// 定義はどちらの Set にも読み込み済みで、会話ごとに違うのは「どれを有効に
// してあるか」だけである。
//
// 共通を先に見るのは、探索パスの「先に書いたものが先勝ち」と同じ向きである。
// ID は共通とチームを通して一意に保たれる (作るときに断る) ので、この順序が
// 効くのは手でファイルを置いたときだけになる。
func Load(set, teamSet *agent.Set, sess *store.Session) *Roster {
	r := &Roster{}
	seen := map[string]bool{}

	for _, id := range sess.Members {
		if seen[id] {
			continue
		}
		seen[id] = true

		if a, ok := set.Get(id); ok {
			r.Members = append(r.Members, &Member{Agent: a, Scope: ScopeCommon})
			continue
		}
		if a, ok := teamSet.Get(id); ok {
			r.Members = append(r.Members, &Member{Agent: a, Scope: ScopeTeam})
			continue
		}
		// 定義が消えた相手は名簿から落ちる。黙って人数を減らさず、何が
		// 欠けたのかを出す。#528664 の「開けるが続行できない」と同じ扱い。
		r.Errors = append(r.Errors, agent.LoadError{Path: id,
			Reason: fmt.Sprintf("有効にしているエージェント %q の定義が見つかりません", id)})
	}

	sortMembers(r.Members)
	return r
}

func sortMembers(list []*Member) {
	byID := make(map[string]*agent.Agent, len(list))
	order := make([]string, 0, len(list))
	for _, m := range list {
		byID[m.ID] = m.Agent
		order = append(order, m.ID)
	}
	agent.SortIDs(order, byID)

	at := make(map[string]*Member, len(list))
	for _, m := range list {
		at[m.ID] = m
	}
	for i, id := range order {
		list[i] = at[id]
	}
}

// Get は ID で引く。
func (r *Roster) Get(id string) (*Member, bool) {
	for _, m := range r.Members {
		if m.ID == id {
			return m, true
		}
	}
	return nil, false
}

// IDs は名簿の ID を並び順に返す。
func (r *Roster) IDs() []string {
	out := make([]string, 0, len(r.Members))
	for _, m := range r.Members {
		out = append(out, m.ID)
	}
	return out
}

// Relation は from から to へ何ができるかを返す。名簿に居ない相手は空。
func (r *Roster) Relation(from, to string) string {
	a, ok := r.Get(from)
	if !ok {
		return ""
	}
	b, ok := r.Get(to)
	if !ok {
		return ""
	}
	return relation(a.Tier, b.Tier)
}

func relation(from, to int) string {
	switch {
	case to < from:
		return RelReport
	case to > from:
		return RelOrder
	}
	return RelRequest
}

// Roll は指示文へ載せる名簿を組み立てる。
//
// 相手の Tier を出して「あとは考えよ」とはしない。数の大小と権限の向きを毎回
// モデルに推論させると、必ずどこかで逆になる。何ができるかを言葉で書く。
func (r *Roster) Roll(self string) string {
	me, ok := r.Get(self)
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nこの会話には次のメンバーが居ます。あなたは %s (Tier %d) です。\n",
		me.ID, me.Tier)
	for _, m := range r.Members {
		if m.ID == self {
			fmt.Fprintf(&b, "- %s (Tier %d, %s): あなた自身\n", m.ID, m.Tier, m.Name)
			continue
		}
		fmt.Fprintf(&b, "- %s (Tier %d, %s): %s ができます。%s\n",
			m.ID, m.Tier, m.Name, relation(me.Tier, m.Tier), describe(m.Agent))
	}
	// 宛先の行き先を書いておく。書かないと、モデルは「まず窓口へ回します」の
	// ような、もう存在しない段取りを自分で補って書き始める。
	b.WriteString("利用者は宛先を名指しして送ります。あなたに届いた依頼は、" +
		"あなたに宛てられたものです。\n")
	return b.String()
}

func describe(a *agent.Agent) string {
	if strings.TrimSpace(a.Description) == "" {
		return "(説明が書かれていません)"
	}
	return strings.Join(strings.Fields(a.Description), " ")
}

// Subordinates は自分より下位のメンバーを返す。
func (r *Roster) Subordinates(self string) []*Member {
	me, ok := r.Get(self)
	if !ok {
		return nil
	}
	var out []*Member
	for _, m := range r.Members {
		if m.Tier > me.Tier {
			out = append(out, m)
		}
	}
	return out
}

// Superiors は自分より上位のメンバーを返す。
func (r *Roster) Superiors(self string) []*Member {
	me, ok := r.Get(self)
	if !ok {
		return nil
	}
	var out []*Member
	for _, m := range r.Members {
		if m.Tier < me.Tier {
			out = append(out, m)
		}
	}
	return out
}

// Guide はチームでの進め方。立場によって書き分ける。
//
// 全員に同じ文を渡すと、下位を持つ相手まで「勝手に始めない」「確認を取る」と
// 読み、部下に許可を求め始める。Tier は指示できる範囲を表すのだから、指示を
// 出す側と受ける側では、進め方の指示そのものが違っていなければならない。
func Guide(r *Roster, self string) string {
	if _, ok := r.Get(self); !ok {
		return ""
	}
	under := r.Subordinates(self)
	over := r.Superiors(self)

	var b strings.Builder
	b.WriteString("\nチームでの進め方:\n")
	b.WriteString("- 相手はこの会話のやり取りを見られません。送る内容は、それ 1 通で読めるように書いてください。\n")
	b.WriteString("- send_message には、なぜそれをすることになったか (誰に何を頼まれたか)、やったこと、\n")
	b.WriteString("  そして相手へのメッセージの 3 つを必ず入れてください。相手が受け取るのはそれだけです。\n")
	b.WriteString("- 同位からの依頼には、受諾か却下かを必ず添えて返します。却下する場合は理由を書いてください。\n")
	b.WriteString("- 手番を終えるときは、誰かへ送るか、何も送らずに終えるかのどちらかです。\n")
	b.WriteString("- 本文に「これから誰々に頼みます」と書いても、それは誰にも届きません。\n")
	b.WriteString("  相手に動いてもらうには、その手番の中で send_message を呼んでください。\n")

	if len(under) > 0 {
		// 下位を持つ側。上司として振る舞わせる。
		fmt.Fprintf(&b, "\nあなたは %d 人の下位を持ちます。あなたはその %d 人の上司です。\n",
			len(under), len(under))
		b.WriteString("- 仕事は分けて、下位へ指示してください。指示は依頼ではありません。相手は断れません。\n")
		b.WriteString("- 配ると決めたら、その手番の中で配り切ってください。次の手番に持ち越すと、\n")
		b.WriteString("  誰も動かないままそのラウンドが終わります。\n")
		b.WriteString("- 下位に許可や確認を求めないでください。やってよいかを決めるのはあなたです。\n")
		b.WriteString("  「〜してもよいですか」「〜で進めてよろしいでしょうか」と部下に問うのは、\n")
		b.WriteString("  指示ではなく責任の押し付けです。何をどこまでやるかを決めて、そのまま伝えてください。\n")
		b.WriteString("- 自分でやったほうが早い小さな作業まで配らないこと。分ける意味のある単位で渡します。\n")
		b.WriteString("- 下位からの報告には応答の義務はありません。読んで、次の指示が要るときだけ出してください。\n")
		b.WriteString("- 上がってきた成果は、あなたが確かめて受け取ります。妥当でなければ、直す点を挙げて指示し直してください。\n")
	} else {
		b.WriteString("\nあなたに下位は居ません。受けた仕事は自分で最後までやり切ります。\n")
		b.WriteString("- 頼まれていないことを勝手に始めないでください。手が空いているなら、その旨を上位へ報告します。\n")
		b.WriteString("- 指示は断れません。やり方に問題があるなら、やったうえで報告に書いてください。\n")
	}

	// 上に誰も居ない人だけが、全体をどう進めるかを決められる。窓口という
	// 指名から出る性質ではなく、Tier の位置から出る性質である。
	if len(over) == 0 {
		b.WriteString("\nあなたより上位のメンバーは居ません。\n")
		b.WriteString("- 利用者はあなたに直接依頼します。全体をどう進めるかを決めるのはあなたです。\n")
	}
	return b.String()
}
