package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/team"
)

// flowOf は会話の図を組み立てる。
func flowOf(t *testing.T, f *fixture, sessionID string) *team.Flow {
	t.Helper()
	sess, err := f.store.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	edges, err := f.store.TeamFlow(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return team.BuildFlow(team.Load(f.eng.Agents, f.eng.TeamAgents, sess), edges)
}

// teamMessages はチームの発言を古い順に返す。
func teamMessages(t *testing.T, f *fixture, sessionID string) []*store.Message {
	t.Helper()
	all, err := f.store.ListMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var out []*store.Message
	for _, m := range all {
		if m.Role == store.RoleTeam || m.Role == provider.RoleUser {
			out = append(out, m)
		}
	}
	return out
}

// openPairs は未完了の矢印を "from→to" で並べる。
func openPairs(f *team.Flow) []string {
	var out []string
	for _, a := range f.Open() {
		out = append(out, a.From+"→"+a.To)
	}
	return out
}

// 送った 1 通は、その手番を始めさせた 1 通に応えたものとして記録される。
func TestReplyToPointsAtWhatStartedTheTurn(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "やって"), {Type: provider.EventDone}}
		}
		if self == "hand" && nth == 0 {
			return []provider.Event{sendTo("lead", "できました"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 頼む", f.emit); err != nil {
		t.Fatal(err)
	}

	msgs := teamMessages(t, f, sess.ID)
	if len(msgs) < 3 {
		t.Fatalf("発言が足りない: %d", len(msgs))
	}
	user, order, report := msgs[0], msgs[1], msgs[2]
	if user.ReplyTo != "" {
		t.Errorf("利用者の発言に応答先が入っている: %q", user.ReplyTo)
	}
	if order.ReplyTo != user.ID {
		t.Errorf("lead の指示の応答先 = %q, want %q", order.ReplyTo, user.ID)
	}
	if report.ReplyTo != order.ID {
		t.Errorf("hand の報告の応答先 = %q, want %q", report.ReplyTo, order.ID)
	}

	// 応えが返ったので、指示はもう未完了ではない。報告は最初から数えない。
	if left := openPairs(flowOf(t, f, sess.ID)); len(left) != 0 {
		t.Errorf("未完了が残っている: %v", left)
	}
}

// 繕った返信は「返事」なので、それ自身は返事待ちにならない。
//
// engine は誰にも送らずに終わった手番の代わりに、依頼元へ 1 通返す
// (#640275)。それを数えると、繕いのたびに永久に返らない矢印が 1 本増え、
// そのうち一覧が繕いで埋まる。
func TestSyntheticRepliesDoNotPileUp(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "やって"), {Type: provider.EventDone}}
		}
		// hand は何も送らずに終える。engine が lead へ繕い、lead もまた
		// 何も送らずに終えるので、engine が hand へ繕う。
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 頼む", f.emit); err != nil {
		t.Fatal(err)
	}

	// lead → hand の指示には繕いが返っているので閉じ、その繕い自身は
	// 返事なので数えない。
	if left := openPairs(flowOf(t, f, sess.ID)); len(left) != 0 {
		t.Errorf("繕いが返事待ちとして残っている: %v", left)
	}
}

// 受けた相手が、頼んだ側へ返さず別の相手へ回したら、その矢印は開いたまま。
//
// 頼んだ側はまだ待っている。engine の繕い (#640275) は依頼元へ返るので
// 閉じるが、本人が別の相手へ回した場合は繕いも走らない。
func TestOrderStaysOpenWhenTheAnswerGoesElsewhere(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		switch {
		case self == "lead" && nth == 0:
			return []provider.Event{sendTo("scout", "レビューして"), {Type: provider.EventDone}}
		case self == "scout" && nth == 0:
			// lead へ返さず、hand へ回す。lead は返事を待ったままになる。
			return []provider.Event{sendTo("hand", "こっちを見て"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 頼む", f.emit); err != nil {
		t.Fatal(err)
	}

	left := openPairs(flowOf(t, f, sess.ID))
	for _, p := range left {
		if p == "lead→scout" {
			return
		}
	}
	t.Errorf("返事の返っていない指示が数えられていない: %v", left)
}

// 宛先が手番を待つ間に外されたら、その矢印は開いたまま残る。実際に
// 届いていないのだから、頼んだ側は待っている。
func TestArrowStaysOpenWhenNobodyRanIt(t *testing.T) {
	var st *store.Store
	var id string
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "やって"), {Type: provider.EventDone}}
		}
		// 配り終えた直後、hand が手番を取る前に名簿から外す。
		if self == "lead" && nth == 1 {
			st.SetMembers(context.Background(), id, []string{"lead", "scout"})
		}
		return done()
	}))
	st, id = f.store, sess.ID

	if err := f.eng.Run(context.Background(), sess.ID, "@lead 頼む", f.emit); err != nil {
		t.Fatal(err)
	}
	// 名簿から消えた相手は Relation が引けないので、未完了にも数えない。
	// 数えられないことと、届いたことは違う — 図には残る。
	var found bool
	for _, a := range flowOf(t, f, sess.ID).Arrows {
		if a.From == "lead" && a.To == "hand" {
			found = true
		}
	}
	if !found {
		t.Error("届かなかった矢印が図から消えている")
	}
}

// ターンは列になる。1 つの手番で 2 人へ配ったら、その 2 人は同じ列に来る。
func TestFanOutLandsInOneColumn(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "先に頼む"), {Type: provider.EventDone}}
		}
		if self == "lead" && nth == 1 {
			return []provider.Event{sendTo("scout", "次に頼む"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}

	flow := flowOf(t, f, sess.ID)
	col := map[string]int{}
	for _, n := range flow.Nodes {
		// 最初に現れた升だけを見る。lead は後ろの列にも出る。
		if _, ok := col[n.ID]; !ok {
			col[n.ID] = n.Col
		}
	}
	if col["hand"] != col["scout"] {
		t.Errorf("列 = hand %d / scout %d (同じ時間軸に居ない)", col["hand"], col["scout"])
	}
	if col["hand"] != col["lead"]+1 {
		t.Errorf("配られた先が lead の隣の列でない (lead %d, hand %d)", col["lead"], col["hand"])
	}
}

// 同じターンで 2 人が同じ相手へ送ったら、升は 1 つで、その相手は 2 回動く。
func TestMergedNodeStillRunsOncePerArrow(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		// lead が 1 つの手番で hand と scout へ配る。どちらも何も返さない
		// ので、engine が lead へ 2 通繕う。
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "先に頼む"), {Type: provider.EventDone}}
		}
		if self == "lead" && nth == 1 {
			return []provider.Event{sendTo("scout", "次に頼む"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}

	// 2 通とも宛先は lead で、同じターンに来る。升は 1 つ。
	// 利用者からの 1 本は 1 ターンめなので、そこは数えない。見たいのは
	// 繕いの 2 本が同じ升に入ることである。
	flow := flowOf(t, f, sess.ID)
	into := map[string]int{}
	for _, a := range flow.Arrows {
		if a.To == "lead" && a.From != "" {
			into[a.ToNode]++
		}
	}
	if len(into) != 1 {
		t.Fatalf("繕いの宛先の升が %d 個に分かれている: %v", len(into), into)
	}
	for _, n := range into {
		if n != 2 {
			t.Errorf("升に入る矢印 = %d, want 2", n)
		}
	}

	// 升は 1 つでも、会話は矢印の本数だけ回る。
	turns := strings.Join(f.turns(), ",")
	if strings.Count(turns, "lead") != 3 {
		t.Errorf("手番 = %s (lead が 1 + 2 回でない)", turns)
	}
}

// 帯に出す手番の番号が、図の列と同じ数え方であること。
func TestTurnNumberIsEmitted(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "やって"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 頼む", f.emit); err != nil {
		t.Fatal(err)
	}

	var got []int
	for _, e := range f.events {
		if e.Type == EvtTurnStart {
			got = append(got, e.Turn)
		}
	}
	if len(got) < 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("ターン番号 = %v, want 1, 2, ...", got)
	}
}
