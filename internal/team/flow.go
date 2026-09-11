package team

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LyraStellate/Ivis/internal/store"
)

// 流れ図 — 連絡の記録と、その時間軸 (#512740)。
//
// チケットが「何の仕事がどこまで進んだか」を持つのに対し、これは「誰が誰に
// 何を頼み、それが返ってきたか」を持つ。各メンバーは自分宛てのやり取りしか
// 読めない (#258413) ので、既に頼んであることは頼んだ本人の履歴の中にしか
// 無く、圧縮で要約に畳まれると消える。そこで重ねて頼むことになる。
//
// **ノードは (ターン, メンバー) である。** 1 ターンめに動いた相手が出した
// 矢印の宛先が 2 ターンめに動き、そこからさらに 3 ターンめが伸びる。同じ
// メンバーでも、ターンが違えば別のノードになる。
//
// ターンは記録から導く。応答先 (reply_to) の連なりがそのまま木になっており、
// 利用者の発言を 1 として何本たどれるかがターン番号である。実行側が番号を
// 持って保存する必要はない。

// FlowNode は図の 1 つの升。
type FlowNode struct {
	// Key は "列:ID"。矢印が指す先である。
	Key string `json:"key"`
	// ID は空なら利用者。store が利用者の発言に空の agent_id を入れるのと
	// 同じ約束で、別の印を作らない。
	ID    string `json:"id"`
	Name  string `json:"name"`
	Tier  int    `json:"tier"`
	Scope string `json:"scope,omitempty"`
	User  bool   `json:"user,omitempty"`
	// Round は何回めの送信から始まった枝か。Turn はそのラウンドの中で
	// 何ターンめか。Col は図の列で、(Round, Turn) の通し番号である。
	Round int `json:"round"`
	Turn  int `json:"turn"`
	Col   int `json:"col"`
	// Waiting はこの升に来ていて、まだ応えていない矢印の数。
	Waiting int `json:"waiting"`
}

// FlowArrow は図の 1 本の矢印。必ず隣の列へ進む。
type FlowArrow struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	FromNode string `json:"from_node"`
	ToNode   string `json:"to_node"`
	Relation string `json:"relation,omitempty"`
	Body     string `json:"body"`
	Decision string `json:"decision,omitempty"`
	Seq      int64  `json:"seq"`
	// Open は、応えが返っていないこと。
	Open bool `json:"open"`
}

// FlowCol は図の 1 列。
type FlowCol struct {
	Col   int `json:"col"`
	Round int `json:"round"`
	Turn  int `json:"turn"`
}

// Flow は 1 つの会話の図。
type Flow struct {
	Cols   []*FlowCol   `json:"cols"`
	Nodes  []*FlowNode  `json:"nodes"`
	Arrows []*FlowArrow `json:"arrows"`
}

// UserName は利用者の升に出す名前。web 側の who.js と同じ語にしてある。
const UserName = "あなた"

// nodeKey は升の名前。
func nodeKey(col int, id string) string { return fmt.Sprintf("%d:%s", col, id) }

// BuildFlow は名簿と矢印の生データから図を組み立てる。
//
// 組み立てを 1 か所に置くのは、画面と指示文が同じものを二度組み立てないため
// である。二度組み立てると、食い違ったときにどちらが正か決められない
// (#189542 が同じ理由で差分をイベントに載せていない)。
func BuildFlow(r *Roster, edges []*store.FlowEdge) *Flow {
	f := &Flow{Cols: []*FlowCol{}, Nodes: []*FlowNode{}, Arrows: []*FlowArrow{}}

	at := map[string]*store.FlowEdge{}
	for _, e := range edges {
		at[e.ID] = e
	}

	// ラウンドとターンを決める。利用者の発言 (応答先が空) が根で 1 ターンめ。
	// 親が見つからないものも根として扱う — 巻き戻しで親だけが消えることは
	// 無いはずだが、そうなっても図が消えるよりは、根が増えるほうがよい。
	round := map[string]int{}
	turn := map[string]int{}
	rounds := 0
	for _, e := range edges {
		p, ok := at[e.ReplyTo]
		if !ok {
			rounds++
			round[e.ID], turn[e.ID] = rounds, 1
			continue
		}
		round[e.ID], turn[e.ID] = round[p.ID], turn[p.ID]+1
	}

	// 列は (ラウンド, ターン) の順に通しで並べる。2 ラウンドめのターン 1 を
	// 1 列めに重ねると、別のラウンドの矢印が同じ列に混ざる。
	type rt struct{ round, turn int }
	seen := map[rt]bool{}
	var order []rt
	for _, e := range edges {
		// 矢印は「送り手の列」から「宛先の列」へ進む。送り手の列も要る。
		for _, k := range []rt{{round[e.ID], turn[e.ID] - 1}, {round[e.ID], turn[e.ID]}} {
			if k.turn >= 0 && !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].round != order[j].round {
			return order[i].round < order[j].round
		}
		return order[i].turn < order[j].turn
	})
	col := map[rt]int{}
	for i, k := range order {
		col[k] = i
		f.Cols = append(f.Cols, &FlowCol{Col: i, Round: k.round, Turn: k.turn})
	}

	// 応えの届いた矢印を集める。応えた側が応答先を持つので、巻き戻しでその行が
	// 消えれば、矢印は自然に開き直る。
	//
	// 応えとは「返事」のことである。受けた仕事を別の相手へ回しただけの矢印も
	// 同じ応答先を持つが、それでは頼んだ側に何も返っていない。
	answered := map[string]bool{}
	for _, e := range edges {
		if isReply(e, at) {
			answered[e.ReplyTo] = true
		}
	}

	nodes := map[string]*FlowNode{}
	node := func(c int, id string, rd, tn int) *FlowNode {
		key := nodeKey(c, id)
		if n, ok := nodes[key]; ok {
			return n
		}
		n := &FlowNode{Key: key, ID: id, Name: UserName, User: id == "",
			Round: rd, Turn: tn, Col: c}
		if m, ok := r.Get(id); ok {
			n.Name, n.Tier, n.Scope = m.Name, m.Tier, m.Scope
		} else if id != "" {
			// 名簿から消えた相手。升は残す — 届かなかったことと、無かった
			// ことは違う。
			n.Name = id
		}
		nodes[key] = n
		f.Nodes = append(f.Nodes, n)
		return n
	}

	for _, e := range edges {
		rd, tn := round[e.ID], turn[e.ID]
		src := node(col[rt{rd, tn - 1}], e.From, rd, tn-1)
		dst := node(col[rt{rd, tn}], e.To, rd, tn)

		a := &FlowArrow{ID: e.ID, From: e.From, To: e.To,
			FromNode: src.Key, ToNode: dst.Key, Body: e.Body,
			Decision: e.Decision, Seq: e.Seq, Relation: r.Relation(e.From, e.To)}
		a.Open = isOpen(a, e, at, answered)
		if a.Open {
			dst.Waiting++
		}
		f.Arrows = append(f.Arrows, a)
	}
	return f
}

// isReply は、その矢印が「返事」かどうかを返す。
//
// 返事とは、応えた相手 (親の矢印の送り手) へ書き戻したもののことである。
// 受けた仕事を別の相手へ回した矢印も同じ応答先を持つが、それは返事ではない
// — 頼んだ側には何も返っていない。
func isReply(e *store.FlowEdge, at map[string]*store.FlowEdge) bool {
	p, ok := at[e.ReplyTo]
	return ok && p.From == e.To
}

// isOpen は、その矢印が返事を待っているかを返す。
//
// 数えるのは、応えの義務がある向きだけである。
//
//   - **報告は数えない。** 上位への報告に応答の義務はない (#640275)。数えると
//     全部の報告が「返事待ち」として永久に溜まる。
//   - **利用者からの矢印も数えない。** 名簿の外へは返せない。どちらかが名簿から
//     消えていれば Relation が空になり、これも数から外れる。
//   - **返事は数えない。** 返事に返事は要らない。
//
// 最後の 1 つが効きどころで、これがあるので engine が繕った返信は自動的に
// 外れる。一方、報告を読んで**別の相手へ**出した指示は、返事ではなく新しい
// 依頼として正しく数えられる。
func isOpen(a *FlowArrow, e *store.FlowEdge, at map[string]*store.FlowEdge,
	answered map[string]bool) bool {
	switch a.Relation {
	case RelOrder, RelRequest:
	default:
		return false
	}
	return !answered[e.ID] && !isReply(e, at)
}

// Open は未完了の矢印を古い順に返す。
func (f *Flow) Open() []*FlowArrow {
	var out []*FlowArrow
	for _, a := range f.Arrows {
		if a.Open {
			out = append(out, a)
		}
	}
	return out
}

// flowNoteMax は指示文に載せる件数の上限。
//
// チーム向けの指示文は既に 3 KB を超えており、毎回の生成で送り直される。
// ここは ticketNote の前置きと同じ桁 (数百バイト) に収める。
const flowNoteMax = 12

// flowBodyMax は本文の抜粋の長さ。
const flowBodyMax = 40

// FlowNote は指示文へ載せる全体図。未完了だけを、新しい順に載せる。
//
// 他人どうしの矢印には本文を載せない。載せると、自分宛てのやり取りしか
// 読めないという原則 (#258413) が崩れる。誰から誰へ・何の種別かまでなら、
// 重ねて頼まないために要る最小限であり、内容そのものは渡らない。
func FlowNote(f *Flow, self string) string {
	open := f.Open()
	if len(open) == 0 {
		return "\nいま返事が返っていないやり取りはありません。\n"
	}

	var b strings.Builder
	b.WriteString("\nいま返事が返っていないやり取り:\n")

	shown := open
	extra := 0
	if len(shown) > flowNoteMax {
		extra = len(shown) - flowNoteMax
		shown = shown[len(shown)-flowNoteMax:]
	}
	// 新しい順。古いものほど、もう手が離れている見込みが高い。
	for i := len(shown) - 1; i >= 0; i-- {
		a := shown[i]
		switch {
		case a.From == self:
			fmt.Fprintf(&b, "- あなた → %s %s「%s」\n", a.To, a.Relation, clip(a.Body, flowBodyMax))
		case a.To == self:
			fmt.Fprintf(&b, "- %s → あなた %s「%s」\n", a.From, a.Relation, clip(a.Body, flowBodyMax))
		default:
			fmt.Fprintf(&b, "- %s → %s %s (返事待ち)\n", a.From, a.To, a.Relation)
		}
	}
	if extra > 0 {
		fmt.Fprintf(&b, "- ほか %d 件\n", extra)
	}
	b.WriteString("上に載っているものは既に頼んであります。同じ相手へ同じことを重ねて頼まないでください。\n")
	return b.String()
}

// clip は 1 行に畳んで n 文字までにする。
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
