// Package team はチームセッションの名簿を組み立てる。
//
// 名簿は 2 つの由来を持つ 1 つの一覧である。参加している共通エージェント
// (定義は config.AgentPaths にあり、直せば参加している全てのチームに効く) と、
// そのセッションのためだけに作られた固有エージェント (定義はセッションの下に
// あり、会話が消えれば一緒に消える) である。
//
// 名簿の中では両者を区別しない。誰に何を頼めるかは Tier だけで決まり、定義が
// どこにあるかは関係しないためである (#731906)。
package team

import (
	"fmt"
	"strings"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
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

// Anyone は宛先を決めずに送るときの印。窓口が受け取り、窓口が回す。
const Anyone = "*"

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

// Member は名簿の 1 人。
type Member struct {
	*agent.Agent
	// Local が真なら、このセッションのためだけに作られた定義である。
	Local bool `json:"local"`
}

// Roster は 1 つのチームセッションの名簿。
type Roster struct {
	// Members は Tier 順、同じ Tier では ID 順。上位から下位へ読める並びは、
	// そのまま指示の流れの向きである。
	Members []*Member
	// LeadID は窓口。宛先の書かれていない発言と "*" はここへ届く。
	LeadID string
	// Errors は読めなかった定義。1 体壊れても残りは名簿に載る。
	Errors []agent.LoadError
}

// Load は会話の名簿を組み立てる。
//
// 固有の定義を先に読み、共通は参加しているものだけを後から足す。ID が衝突
// したときに固有を残すのは、その会話のために意図して作られたものだから
// である。衝突は失敗として記録し、黙って片方を消さない。
func Load(cfg *config.Config, set *agent.Set, sess *store.Session) *Roster {
	r := &Roster{LeadID: sess.AgentID}
	seen := map[string]bool{}

	locals, errs := agent.ReadDir(cfg.SessionAgentsDir(sess.ID))
	r.Errors = append(r.Errors, errs...)
	for _, a := range locals {
		if seen[a.ID] {
			r.Errors = append(r.Errors, agent.LoadError{Path: a.File,
				Reason: fmt.Sprintf("エージェント ID %q がこの会話の中で重複しています", a.ID)})
			continue
		}
		seen[a.ID] = true
		r.Members = append(r.Members, &Member{Agent: a, Local: true})
	}

	for _, id := range sess.Members {
		if seen[id] {
			// 固有の定義が同じ名前を取っている。会話の中で宛先が一意に
			// 決まらなくなるので、参加している側を載せない。
			r.Errors = append(r.Errors, agent.LoadError{Path: id,
				Reason: fmt.Sprintf("共通エージェント %q は、この会話の固有エージェントと名前が重なっています", id)})
			continue
		}
		a, ok := set.Get(id)
		if !ok {
			r.Errors = append(r.Errors, agent.LoadError{Path: id,
				Reason: fmt.Sprintf("参加している共通エージェント %q の定義が見つかりません", id)})
			continue
		}
		seen[id] = true
		r.Members = append(r.Members, &Member{Agent: a})
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

// Lead は窓口を返す。定義が外れていれば見つからない。
func (r *Roster) Lead() (*Member, bool) { return r.Get(r.LeadID) }

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
	if r.LeadID != "" {
		fmt.Fprintf(&b, "窓口は %s です。宛先の書かれていない依頼はそこへ届きます。\n", r.LeadID)
	}
	return b.String()
}

func describe(a *agent.Agent) string {
	if strings.TrimSpace(a.Description) == "" {
		return "(説明が書かれていません)"
	}
	return strings.Join(strings.Fields(a.Description), " ")
}

// Guide はチームでの進め方。名簿とは別に書くのは、名簿が会話ごとに変わる
// のに対し、こちらは常に同じだからである。
func Guide(self string, canOrderAnyone bool) string {
	var b strings.Builder
	b.WriteString("\nチームでの進め方:\n")
	b.WriteString("- 相手はこの会話のやり取りを見られません。送る内容は、それ 1 通で読めるように書いてください。\n")
	b.WriteString("- send_message には、なぜそれをすることになったか (誰に何を頼まれたか)、やったこと、\n")
	b.WriteString("  そして相手へのメッセージの 3 つを必ず入れてください。相手が受け取るのはそれだけです。\n")
	b.WriteString("- 同位からの依頼には、受諾か却下かを必ず添えて返します。却下する場合は理由を書いてください。\n")
	b.WriteString("- 上位への報告に応答の義務はありません。読んで、必要があれば動いてください。\n")
	b.WriteString("- 手番を終えるときは、誰かへ送るか、何も送らずに終えるかのどちらかです。\n")
	b.WriteString("- 頼まれていないことを勝手に始めないでください。手が空いているなら、その旨を上位へ報告します。\n")
	if canOrderAnyone {
		b.WriteString("- 宛先が決められないときは \"*\" を指定できます。窓口が引き受けて回します。\n")
	}
	return b.String()
}
