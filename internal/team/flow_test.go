package team

import (
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/store"
)

// flowRoster は lead (Tier 1) / hand (Tier 2) / peer (Tier 2) の 3 人。
func flowRoster() *Roster {
	r := &Roster{}
	for _, m := range []struct {
		id   string
		tier int
	}{{"lead", 1}, {"hand", 2}, {"peer", 2}} {
		r.Members = append(r.Members, &Member{
			Agent: &agent.Agent{ID: m.id, Name: m.id, Tier: m.tier}, Scope: ScopeTeam})
	}
	return r
}

var seq int64

func edge(id, from, to, body, replyTo string) *store.FlowEdge {
	seq++
	return &store.FlowEdge{ID: id, From: from, To: to, Body: body, ReplyTo: replyTo, Seq: seq}
}

// build は seq を振り直してから組み立てる。
func build(edges ...*store.FlowEdge) *Flow {
	return BuildFlow(flowRoster(), edges)
}

func arrowByID(f *Flow, id string) *FlowArrow {
	for _, a := range f.Arrows {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func nodeByKey(f *Flow, key string) *FlowNode {
	for _, n := range f.Nodes {
		if n.Key == key {
			return n
		}
	}
	return nil
}

// 利用者の発言が 1 ターンめ。その宛先が出した矢印が 2 ターンめになる。
func TestTurnsCountFromTheUsersMessage(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "認証を作って", ""),
		edge("m2", "lead", "hand", "実装して", "m1"),
		edge("m3", "hand", "lead", "できました", "m2"),
	)
	want := map[string]int{"m1": 1, "m2": 2, "m3": 3}
	for id, w := range want {
		a := arrowByID(f, id)
		n := nodeByKey(f, a.ToNode)
		if n.Turn != w {
			t.Errorf("%s の宛先のターン = %d, want %d", id, n.Turn, w)
		}
	}

	// 矢印は必ず隣の列へ進む。戻る矢印は無い。
	for _, a := range f.Arrows {
		src, dst := nodeByKey(f, a.FromNode), nodeByKey(f, a.ToNode)
		if dst.Col != src.Col+1 {
			t.Errorf("%s が %d 列から %d 列へ跳んでいる", a.ID, src.Col, dst.Col)
		}
	}

	// 報告は前の升へ戻らず、同じ行の次の列に出る。
	if a := arrowByID(f, "m3"); nodeByKey(f, a.ToNode).ID != "lead" {
		t.Error("報告の宛先が lead でない")
	}
	if nodeByKey(f, arrowByID(f, "m2").FromNode).Key ==
		nodeByKey(f, arrowByID(f, "m3").ToNode).Key {
		t.Error("報告が lead の元の升へ戻っている")
	}
}

// 1 つの手番で 2 人へ配ったら、その 2 人は同じ列に来る。
func TestOneTurnFansOutIntoOneColumn(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", "実装して", "m1"),
		edge("m3", "lead", "peer", "要件を出して", "m1"),
	)
	hand := nodeByKey(f, arrowByID(f, "m2").ToNode)
	peer := nodeByKey(f, arrowByID(f, "m3").ToNode)
	if hand.Col != peer.Col {
		t.Errorf("列 = hand %d / peer %d (同じ時間軸に居ない)", hand.Col, peer.Col)
	}
}

// 同じターンで 2 人が同じ相手へ送ったら、升は 1 つで、矢印が 2 本入る。
func TestArrowsInTheSameTurnMergeIntoOneNode(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", "実装して", "m1"),
		edge("m3", "lead", "peer", "見て", "m1"),
		// hand と peer は同じターン。どちらも次のターンの lead へ返す。
		edge("m4", "hand", "lead", "できました", "m2"),
		edge("m5", "peer", "lead", "見ました", "m3"),
	)
	a, b := arrowByID(f, "m4"), arrowByID(f, "m5")
	if a.ToNode != b.ToNode {
		t.Fatalf("宛先の升が分かれている: %s / %s", a.ToNode, b.ToNode)
	}
	n := 0
	for _, x := range f.Arrows {
		if x.ToNode == a.ToNode {
			n++
		}
	}
	if n != 2 {
		t.Errorf("升に入る矢印 = %d, want 2", n)
	}
}

// 2 回めの送信の列は、1 回めの全部より右に来る。
func TestARoundStartsToTheRightOfTheLastOne(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "1 回め", ""),
		edge("m2", "lead", "hand", "配る", "m1"),
		edge("m3", "", "lead", "2 回め", ""),
	)
	first := nodeByKey(f, arrowByID(f, "m2").ToNode)
	second := nodeByKey(f, arrowByID(f, "m3").ToNode)
	if second.Col <= first.Col {
		t.Errorf("2 ラウンドめの列 %d が、1 ラウンドめの %d より右でない", second.Col, first.Col)
	}
	// 見出しはラウンドごとに 1 から数え直す。
	if second.Turn != 1 || second.Round != 2 {
		t.Errorf("2 ラウンドめ = ラウンド %d ターン %d", second.Round, second.Turn)
	}
}

// 応えの返っていない指示だけが未完了になる。
func TestOpenArrowsAreUnansweredOrders(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "認証を作って", ""),
		edge("m2", "lead", "hand", "実装して", "m1"),
		edge("m3", "lead", "peer", "要件を出して", "m1"),
		edge("m4", "peer", "lead", "まとめました", "m3"),
	)
	want := map[string]bool{
		"m1": false, // 利用者からの矢印。名簿の外へは返せない
		"m2": true,  // 指示。まだ返っていない
		"m3": false, // 応えが返った
		"m4": false, // 報告。応答の義務がない
	}
	for id, w := range want {
		if got := arrowByID(f, id).Open; got != w {
			t.Errorf("%s の未完了 = %v, want %v", id, got, w)
		}
	}
}

// 親の送り手へ書き戻したものは「返事」であり、返事に返事は要らない。
//
// engine が繕った返信がこれにあたる。ここを数えると、繕いのたびに永久に
// 返らない矢印が 1 本増え、そのうち一覧が繕いで埋まる。
func TestAReplyDoesNotAwaitAReply(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", "実装して", "m1"),
		edge("m3", "hand", "lead", "できました", "m2"),
		// lead は hand の報告に応えて hand へ書き戻す (繕い)。宛先が
		// 親の送り手と同じなので、これは返事である。
		edge("m4", "lead", "hand", "了解しました", "m3"),
	)
	if arrowByID(f, "m4").Open {
		t.Error("返事が返事待ちとして数えられている")
	}
}

// 報告を読んで別の相手へ出した指示は、返事ではなく新しい依頼である。
func TestAnOrderToSomeoneElseIsNotAReply(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", "実装して", "m1"),
		edge("m3", "hand", "lead", "できました", "m2"),
		// hand の報告を読んで、lead が peer へ新しい指示を出す。
		edge("m4", "lead", "peer", "レビューして", "m3"),
	)
	if !arrowByID(f, "m4").Open {
		t.Error("別の相手への新しい指示が未完了に数えられていない")
	}
}

// 名簿から消えた相手との矢印は未完了に数えない。応えられる相手が居ない。
// ただし升は残す — 届かなかったことと、無かったことは違う。
func TestArrowToMissingMemberIsNotOpenButStaysDrawn(t *testing.T) {
	f := build(edge("m1", "lead", "gone", "やって", ""))
	a := arrowByID(f, "m1")
	if a.Open {
		t.Error("名簿に居ない相手への矢印が未完了になっている")
	}
	if nodeByKey(f, a.ToNode) == nil {
		t.Error("届かなかった矢印の升が図から消えている")
	}
}

// 升は自分に来ている未完了の数を持つ。
func TestNodesCountWhatTheyOwe(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", "1 つ目", "m1"),
		edge("m3", "lead", "peer", "2 つ目", "m1"),
		edge("m4", "peer", "lead", "返します", "m3"),
	)
	if n := nodeByKey(f, arrowByID(f, "m2").ToNode); n.Waiting != 1 {
		t.Errorf("hand の状況 = %d, want 1", n.Waiting)
	}
	if n := nodeByKey(f, arrowByID(f, "m3").ToNode); n.Waiting != 0 {
		t.Errorf("peer の状況 = %d, want 0 (応えている)", n.Waiting)
	}
}

// 矢印が 1 本も無ければ、升も列も無い。
func TestEmptyFlow(t *testing.T) {
	f := build()
	if len(f.Nodes) != 0 || len(f.Cols) != 0 || len(f.Arrows) != 0 {
		t.Fatalf("空でない: %+v", f)
	}
}

// 指示文には未完了だけが載る。
func TestFlowNoteListsOnlyOpen(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", "実装して", "m1"),
		edge("m3", "lead", "peer", "要件を出して", "m1"),
		edge("m4", "peer", "lead", "まとめました", "m3"),
	)
	note := FlowNote(f, "lead")
	if !strings.Contains(note, "実装して") {
		t.Errorf("未完了が載っていない: %q", note)
	}
	if strings.Contains(note, "要件を出して") || strings.Contains(note, "まとめました") {
		t.Errorf("済んだ矢印が載っている: %q", note)
	}
}

// 他人どうしの矢印には本文を載せない。自分宛てのやり取りしか読めないと
// いう原則を、全体図で回り込んで崩してはならない。
func TestFlowNoteHidesOtherPeoplesBodies(t *testing.T) {
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", "秘密の段取り", "m1"),
	)
	note := FlowNote(f, "peer")
	if strings.Contains(note, "秘密の段取り") {
		t.Errorf("他人どうしの本文が漏れている: %q", note)
	}
	if !strings.Contains(note, "lead → hand") {
		t.Errorf("誰から誰へが載っていない: %q", note)
	}
}

// 0 件のときは 1 行だけにする。空欄を出すと、仕組みごと忘れられる。
func TestFlowNoteWhenNothingIsOpen(t *testing.T) {
	note := FlowNote(build(), "lead")
	if !strings.Contains(note, "ありません") {
		t.Errorf("note = %q", note)
	}
	if strings.Count(strings.TrimSpace(note), "\n") != 0 {
		t.Errorf("1 行に収まっていない: %q", note)
	}
}

// 上限を超えたら、新しいものから載せて残りは件数にする。
func TestFlowNoteCapsTheList(t *testing.T) {
	edges := []*store.FlowEdge{edge("root", "", "lead", "やって", "")}
	for i := 0; i < flowNoteMax+3; i++ {
		edges = append(edges, edge(string(rune('a'+i)), "lead", "hand", "指示", "root"))
	}
	note := FlowNote(BuildFlow(flowRoster(), edges), "lead")
	if n := strings.Count(note, "- あなた → hand"); n != flowNoteMax {
		t.Errorf("載った件数 = %d, want %d", n, flowNoteMax)
	}
	if !strings.Contains(note, "ほか 3 件") {
		t.Errorf("省いた件数が出ていない: %q", note)
	}
}

// 本文は 1 行に畳んで切る。指示文は毎回の生成で送り直される。
func TestFlowNoteClipsTheBody(t *testing.T) {
	long := strings.Repeat("あ", 200)
	f := build(
		edge("m1", "", "lead", "やって", ""),
		edge("m2", "lead", "hand", long, "m1"),
	)
	note := FlowNote(f, "lead")
	if !strings.Contains(note, "…") {
		t.Errorf("切られていない: %q", note)
	}
	if len([]rune(note)) > 200 {
		t.Errorf("長すぎる: %d 文字", len([]rune(note)))
	}
}
