package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
)

// teamFixture は lead (Tier 1, 窓口) / hand (Tier 2) / scout (Tier 2) の 3 人を
// 足したチームセッションを作る。
func teamFixture(t *testing.T, script func(n int, req provider.Request) []provider.Event) (*fixture, *store.Session) {
	t.Helper()
	f := newFixture(t, script)
	for _, a := range []struct {
		id   string
		tier int
	}{{"lead", 1}, {"hand", 2}, {"scout", 2}} {
		writeAgent(t, f.agentsDir, a.id, map[string]any{
			"name":  a.id,
			"model": "mock-model",
			// 指示文の先頭はそのまま定義の指示文である。ここに ID を置くと、
			// 届いた要求から誰の手番かが分かる。
			"instructions": a.id,
			"tier":         a.tier,
			"tools":        []string{"send_message", "list_dir", "list_tickets", "create_ticket", "update_ticket"},
			"skills":       []string{},
		})
	}
	f.reload()

	sess, err := f.store.CreateTeamSession(context.Background(), "lead", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetMembers(context.Background(), sess.ID,
		[]string{"lead", "hand", "scout"}); err != nil {
		t.Fatal(err)
	}
	sess, _ = f.store.GetSession(context.Background(), sess.ID)
	return f, sess
}

// byMember は「誰の何回目の生成か」で脚本を書けるようにする。
//
// 生成の通し番号で書くと、1 つの手番が何回生成するか (ツールを呼べば 2 回に
// なる) に脚本が依存し、手番の境目とずれる。
func byMember(fn func(self string, nth int) []provider.Event) func(int, provider.Request) []provider.Event {
	count := map[string]int{}
	return func(_ int, req provider.Request) []provider.Event {
		self := whoAmI(req)
		n := count[self]
		count[self]++
		return fn(self, n)
	}
}

// whoAmI は要求から手番の主を読む。指示文の先頭行が定義の指示文であり、
// そこに ID を置いてある。
func whoAmI(req provider.Request) string {
	if len(req.Messages) == 0 {
		return ""
	}
	return strings.SplitN(req.Messages[0].Content, "\n", 2)[0]
}

// sendTo は send_message の呼び出しを 1 つ作る。
func sendTo(to, body string, extra ...string) provider.Event {
	a := map[string]any{"to": to, "why": "頼まれた", "did": "やった", "message": body}
	for i := 0; i+1 < len(extra); i += 2 {
		a[extra[i]] = extra[i+1]
	}
	return callTool("send_message", a)
}

func done() []provider.Event {
	return []provider.Event{text("終わり"), {Type: provider.EventDone}}
}

// 誰の手番だったかを、走った順に返す。
func (f *fixture) turns() []string {
	var out []string
	for _, e := range f.events {
		if e.Type == EvtTurnStart {
			out = append(out, e.AgentID)
		}
	}
	return out
}

// 1 つの手番で 2 人へ送ったら、送った順に手番が回る。
func TestRoundRunsTurnsInOrder(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		// lead は 1 つの手番で 2 人へ配る。
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

	// hand と scout は何も返さずに終わるので、engine が代わりに lead へ
	// 返す。その分だけ lead の手番が後ろに 2 つ増える。
	got := strings.Join(f.turns(), ",")
	if got != "lead,hand,scout,lead,lead" {
		t.Errorf("手番の順 = %s, want lead,hand,scout,lead,lead", got)
	}
	if f.events[len(f.events)-1].Type != EvtDone {
		t.Error("ラウンドが終わっていない")
	}
}

// 宛先は利用者が名指しする。書いた宛先は本文に残さない。
func TestMentionRouting(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@hand これを頼む", f.emit); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.turns(), ","); got != "hand" {
		t.Errorf("宛先 = %s, want hand", got)
	}
	msgs, _ := f.store.ListMessages(context.Background(), sess.ID)
	if msgs[0].Content != "これを頼む" {
		t.Errorf("保存 = %q (宛先の記述が残っている)", msgs[0].Content)
	}
	if msgs[0].ToAgentID != "hand" {
		t.Errorf("宛先の記録 = %q", msgs[0].ToAgentID)
	}
}

// 宛先を書かずに送ったら断る。ここで誰かを選ぶと、それは窓口を別の名前で
// 復活させたことになる (#640275)。
func TestNoAddresseeIsRefused(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return done()
	}))
	err := f.eng.Run(context.Background(), sess.ID, "誰かやって", f.emit)
	if err == nil {
		t.Fatal("宛先なしで通ってしまう")
	}
	if !errors.Is(err, ErrNoAddressee) {
		t.Errorf("種類が違う: %v", err)
	}
	// 誰に頼めるのかを添える。添えないと、次に何を打てばよいか分からない。
	for _, want := range []string{"lead", "hand", "scout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("名簿が添えられていない: %v", err)
		}
	}
	if len(f.turns()) != 0 {
		t.Error("断ったのに手番が始まっている")
	}
	// 断る位置は保存の前。保存してから断ると、宛先の無い発言が履歴に溜まる。
	msgs, _ := f.store.ListMessages(context.Background(), sess.ID)
	if len(msgs) != 0 {
		t.Errorf("断ったのに保存されている: %d 件", len(msgs))
	}
}

// 名簿が 1 人なら迷う余地が無い。書かせる理由も無い。
func TestSoloTeamNeedsNoAddressee(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return done()
	}))
	if err := f.store.SetMembers(context.Background(), sess.ID, []string{"hand"}); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.Run(context.Background(), sess.ID, "やって", f.emit); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.turns(), ","); got != "hand" {
		t.Errorf("宛先 = %s, want hand", got)
	}
}

// 居ない相手を指したら、手番は始まらない。
func TestUnknownMentionIsRefused(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return done()
	}))
	err := f.eng.Run(context.Background(), sess.ID, "@nobody やって", f.emit)
	if err == nil {
		t.Fatal("居ない相手への宛先が通ってしまう")
	}
	if !strings.Contains(err.Error(), "居ません") {
		t.Errorf("理由 = %q", err.Error())
	}
}

// 互いに送り合っても、どこかで必ず止まる。止めたことは失敗ではなく知らせで
// 出し、残っている宛先を見せる。
func TestRoundStopsAtLimit(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		// 手番の 1 回目で相手へ投げ返し、2 回目 (道具の結果を読んだ後) で終える。
		if nth%2 == 1 {
			return done()
		}
		to := "scout"
		if self == "scout" {
			to = "hand"
		}
		return []provider.Event{sendTo(to, "そちらで", "decision", "受諾"), {Type: provider.EventDone}}
	}))
	f.cfg.MaxTurns = 5

	if err := f.eng.Run(context.Background(), sess.ID, "@hand 頼む", f.emit); err != nil {
		t.Fatal(err)
	}
	if n := len(f.turns()); n != 5 {
		t.Errorf("手番の数 = %d, want 5", n)
	}
	var notice string
	for _, e := range f.events {
		if e.Type == EvtNotice {
			notice = e.Text
		}
	}
	if !strings.Contains(notice, "上限") || !strings.Contains(notice, "残っている宛先") {
		t.Errorf("打ち切りの知らせが出ていない: %q", notice)
	}
	for _, e := range f.events {
		if e.Type == EvtError {
			t.Error("打ち切りが失敗として出ている")
		}
	}
}

// 手番を待つ間に外された相手の分は捨て、次へ進む。
func TestRemovedMemberIsSkipped(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("scout", "頼む"), {Type: provider.EventDone}}
		}
		return done()
	}))
	// lead が scout へ送った直後に、scout を外す。
	orig := f.emit
	emit := func(ev Event) {
		orig(ev)
		if ev.Type == EvtTeamMessage && ev.To == "scout" {
			_ = f.store.SetMembers(context.Background(), sess.ID, []string{"lead", "hand"})
		}
	}
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", emit); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.turns(), ","); got != "lead" {
		t.Errorf("手番 = %s, want lead だけ", got)
	}
	var notice string
	for _, e := range f.events {
		if e.Type == EvtNotice {
			notice = e.Text
		}
	}
	if !strings.Contains(notice, "居ません") {
		t.Errorf("届かなかったことが伝わっていない: %q", notice)
	}
}

// 1 人が失敗してもラウンドは止まらない。依頼元へ失敗を返して次へ進む。
func TestOneFailureDoesNotStopRound(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "頼む"), {Type: provider.EventDone}}
		}
		if self == "hand" {
			return []provider.Event{{Type: provider.EventError, Err: errBroken}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	// lead → hand (失敗) → lead (失敗の報告) と回る。
	if got := strings.Join(f.turns(), ","); got != "lead,hand,lead" {
		t.Errorf("手番 = %s, want lead,hand,lead", got)
	}
	if f.events[len(f.events)-1].Type != EvtDone {
		t.Error("ラウンドが終わっていない")
	}
}

// 受け手が読むのは自分宛てのやり取りだけ。隣の道具の往復は入らない。
func TestTurnInputIsScopedToSelf(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{callTool("list_dir", map[string]any{"path": "."})}
		}
		if self == "lead" && nth == 1 {
			return []provider.Event{sendTo("hand", "調べて"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 秘密の依頼", f.emit); err != nil {
		t.Fatal(err)
	}

	got := inputOf(f, "hand")
	if !strings.Contains(got, "調べて") {
		t.Error("自分宛てのメッセージが渡っていない")
	}
	if !strings.Contains(got, "lead からの指示") {
		t.Errorf("誰からの何なのかが渡っていない:\n%s", got)
	}
	if strings.Contains(got, "秘密の依頼") {
		t.Error("自分宛てでない利用者の依頼が渡っている")
	}
	if strings.Contains(got, "list_dir") {
		t.Error("ほかの人の道具の往復が渡っている")
	}
}

// inputOf はそのメンバーへ渡された入力 (指示文を除く) を 1 つの文にする。
func inputOf(f *fixture, self string) string {
	var b strings.Builder
	for _, req := range f.mock.reqs {
		if whoAmI(req) != self {
			continue
		}
		for _, m := range req.Messages {
			if m.Role != provider.RoleSystem {
				b.WriteString(m.Content + "\n")
			}
		}
	}
	return b.String()
}

// 指示文には名簿と、相手ごとに何ができるかが載る。委譲先は載らない。
func TestTeamSystemPrompt(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	sys := f.mock.reqs[0].Messages[0].Content
	for _, want := range []string{"hand", "指示 ができます", "名指し", "チームでの進め方"} {
		if !strings.Contains(sys, want) {
			t.Errorf("指示文に %q が無い", want)
		}
	}
	if strings.Contains(sys, "delegate で呼べます") {
		t.Error("チームなのに委譲先が載っている")
	}
	// 作業場所はチームの段の下。
	if !strings.Contains(sys, "team") {
		t.Errorf("作業場所がチームの段になっていない:\n%s", sys)
	}
}

// チームでは委譲を渡さない。記録に残らない受け渡しがあると、会話が
// 「誰が何をしたか」の記録として信用できなくなる。
func TestDelegateIsNotOfferedInTeam(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	for _, d := range f.mock.reqs[0].Tools {
		if d.Name == "delegate" {
			t.Error("チームに delegate が渡っている")
		}
	}
	var names []string
	for _, d := range f.mock.reqs[0].Tools {
		names = append(names, d.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "send_message") {
		t.Errorf("send_message が渡っていない: %v", names)
	}
}

// 担当しているチケットは、毎ターン指示文に載る。見えていないものは扱われない。
func TestOwnTicketsAppearInPrompt(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return done()
	}))
	ctx := context.Background()
	if err := f.store.CreateTicket(ctx, &store.Ticket{SessionID: sess.ID,
		Title: "lead の仕事", Assignee: "lead"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateTicket(ctx, &store.Ticket{SessionID: sess.ID,
		Title: "hand の仕事", Assignee: "hand"}); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.Run(ctx, sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	sys := f.mock.reqs[0].Messages[0].Content
	if !strings.Contains(sys, "lead の仕事") {
		t.Error("自分の担当が指示文に無い")
	}
	if strings.Contains(sys, "hand の仕事") {
		t.Error("ほかの人の担当が指示文に載っている")
	}
}

// 直列の会話は何も変わらない。
func TestSeriesPromptHasNoTeamNote(t *testing.T) {
	f := newFixture(t, func(n int, req provider.Request) []provider.Event {
		return done()
	})
	id := f.newSession(t, "main")
	if err := f.eng.Run(context.Background(), id, "やって", f.emit); err != nil {
		t.Fatal(err)
	}
	sys := f.mock.reqs[0].Messages[0].Content
	if strings.Contains(sys, "チームでの進め方") {
		t.Error("直列の会話にチームの説明が載っている")
	}
	if !strings.Contains(sys, "series") {
		t.Error("作業場所が直列の段でなくなっている")
	}
	for _, d := range f.mock.reqs[0].Tools {
		if d.Name == "send_message" {
			t.Error("直列の会話に send_message が渡っている")
		}
	}
}

// まとめる文には、誰から誰へのメッセージかが残る。書かないと「誰かが何かを
// 言った」の羅列になり、要約から手番の流れが読めない。
func TestTranscriptNamesBothEnds(t *testing.T) {
	got := transcript([]*store.Message{
		{Role: store.RoleTeam, AgentID: "lead", ToAgentID: "hand", Content: "頼む"},
		{Role: store.RoleTeam, AgentID: "hand", ToAgentID: "lead", Content: "手が空きません",
			Decision: "却下"},
	})
	for _, want := range []string{"lead → hand", "hand → lead", "却下", "頼む"} {
		if !strings.Contains(got, want) {
			t.Errorf("まとめる文に %q が無い:\n%s", want, got)
		}
	}
}

// 誰にも渡さずに終わった手番は、engine が依頼元へ返す。返さないと、頼んだ
// 側は返事の来ないまま待ち、ラウンドはそこで空になって終わる。何を頼んだ
// 仕事がどうなったのかが誰にも分からない (#640275)。
func TestSilentTurnIsAnsweredForYou(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "調べて"), {Type: provider.EventDone}}
		}
		if self == "hand" {
			// 送らずに本文だけ書いて終える。
			return []provider.Event{text("調べ終わりました"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}

	// lead → hand → (繕い) → lead。
	if got := strings.Join(f.turns(), ","); got != "lead,hand,lead" {
		t.Fatalf("手番 = %s, want lead,hand,lead", got)
	}

	var filled *Event
	for i, e := range f.events {
		if e.Type == EvtTeamMessage && e.AgentID == "hand" {
			filled = &f.events[i]
		}
	}
	if filled == nil {
		t.Fatal("繕った返信が流れていない")
	}
	if filled.To != "lead" {
		t.Errorf("宛先 = %q, want lead", filled.To)
	}
	// 書いた本文はそのまま渡す。捨てると、やった仕事の跡が消える。
	if !strings.Contains(filled.Text, "調べ終わりました") {
		t.Errorf("本文が渡っていない: %q", filled.Text)
	}
	// 本人の言葉でないことが分かるようにする。
	if !strings.Contains(filled.Why, AutoNote) {
		t.Errorf("自動の印が無い: %q", filled.Why)
	}

	var notice string
	for _, e := range f.events {
		if e.Type == EvtNotice {
			notice = e.Text
		}
	}
	if !strings.Contains(notice, "代わりに返しました") {
		t.Errorf("繕ったことが利用者に伝わっていない: %q", notice)
	}
}

// 何も書かずに終わった手番でも、頼んだ側には何か返る。空のまま黙るのが
// いちばん困る。
func TestEmptyTurnStillAnswers(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "調べて"), {Type: provider.EventDone}}
		}
		if self == "hand" {
			return []provider.Event{{Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	for _, e := range f.events {
		if e.Type == EvtTeamMessage && e.AgentID == "hand" {
			if !strings.Contains(e.Text, "何も返りませんでした") {
				t.Errorf("何が起きたのかが伝わらない: %q", e.Text)
			}
			return
		}
	}
	t.Error("繕った返信が流れていない")
}

// 繕いに繕いを返さない。返すと、何も進まないまま上限まで往復する。
func TestFillInDoesNotChain(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "調べて"), {Type: provider.EventDone}}
		}
		// 誰も何も送らない。
		return []provider.Event{text("……"), {Type: provider.EventDone}}
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	// lead → hand → (繕い) → lead で終わる。lead が受けた繕いをさらに
	// 繕うと、ここが伸び続ける。
	if got := strings.Join(f.turns(), ","); got != "lead,hand,lead" {
		t.Errorf("手番 = %s, want lead,hand,lead", got)
	}
	if f.events[len(f.events)-1].Type != EvtDone {
		t.Error("ラウンドが終わっていない")
	}
}

// 手番が失敗したときも、依頼元には理由が返る。
func TestFailureIsAnsweredOnce(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "頼む"), {Type: provider.EventDone}}
		}
		if self == "hand" {
			return []provider.Event{{Type: provider.EventError, Err: errBroken}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, e := range f.events {
		if e.Type == EvtTeamMessage && e.AgentID == "hand" {
			n++
			if !strings.Contains(e.Text, "失敗") {
				t.Errorf("理由が返っていない: %q", e.Text)
			}
		}
	}
	if n != 1 {
		t.Errorf("繕った返信 = %d 件, want 1", n)
	}
}

// 上司には上司としての進め方が、部下には部下としての進め方が渡る。
func TestPromptTellsLeadersToInstruct(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "やって"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}

	var lead, hand string
	for _, req := range f.mock.reqs {
		switch whoAmI(req) {
		case "lead":
			lead = req.Messages[0].Content
		case "hand":
			hand = req.Messages[0].Content
		}
	}
	if !strings.Contains(lead, "上司") || !strings.Contains(lead, "許可や確認を求めない") {
		t.Errorf("窓口が上司として振る舞うよう伝わっていない:\n%s", lead)
	}
	if strings.Contains(hand, "上司です") {
		t.Error("下位の居ない相手を上司として扱っている")
	}
}

// 「これから誰々に頼みます」と本文へ書いて終える手番は多い。書いた予定は
// 誰にも届かず、ラウンドはそこで止まる。1 度だけ促して、送らせる (#640275)。
func TestSilentTurnIsNudgedOnce(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			// 予定を書くだけで終える。
			return []provider.Event{text("これより hand へ実装を指示します"), {Type: provider.EventDone}}
		}
		if self == "lead" && nth == 1 {
			// 促されて、はじめて送る。
			return []provider.Event{sendTo("hand", "実装して"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 作って", f.emit); err != nil {
		t.Fatal(err)
	}

	var got *Event
	for i, e := range f.events {
		if e.Type == EvtTeamMessage && e.AgentID == "lead" {
			got = &f.events[i]
		}
	}
	if got == nil {
		t.Fatal("促しても送られていない")
	}
	if got.To != "hand" || !strings.Contains(got.Text, "実装して") {
		t.Errorf("送られた先と本文 = %q / %q", got.To, got.Text)
	}
	// 促しは手番を増やさない。同じ手番の中で続きを書かせる。
	if strings.Join(f.turns(), ",") != "lead,hand,lead" {
		t.Errorf("手番 = %v", f.turns())
	}

	// 1 つの入力に念押しが 2 つ並ばないこと。並ぶなら、送るまで押し続けて
	// いることになる。
	seen, most := false, 0
	for _, req := range f.mock.reqs {
		if whoAmI(req) != "lead" {
			continue
		}
		n := 0
		for _, m := range req.Messages {
			n += strings.Count(m.Content, "まだ誰にもメッセージを送っていません")
		}
		if n > 0 {
			seen = true
		}
		if n > most {
			most = n
		}
	}
	if !seen {
		t.Error("促していない")
	}
	if most > 1 {
		t.Errorf("1 つの入力に促しが %d 個ある", most)
	}

	// 促しは会話に残さない。残すと、次の手番でも読まれて、何度も押されて
	// いるように見える。
	msgs, err := f.store.ListMessages(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "まだ誰にもメッセージを送っていません") {
			t.Error("促しが会話に保存されている")
		}
	}
}

// 送った手番は促さない。押す理由が無く、生成が 1 回増えるだけになる。
func TestSendingTurnIsNotNudged(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "やって"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 作って", f.emit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inputOf(f, "lead"), "まだ誰にもメッセージを送っていません") {
		t.Error("送ったのに促している")
	}
}

// 促しても配らなかった上位は、利用者から見ると何もしていない。ラウンドが
// 黙って終わるので、理由を出す。
func TestSilentLeadTellsTheUser(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		return []provider.Event{text("考えています"), {Type: provider.EventDone}}
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 作って", f.emit); err != nil {
		t.Fatal(err)
	}
	var notice string
	for _, e := range f.events {
		if e.Type == EvtNotice {
			notice = e.Text
		}
	}
	if !strings.Contains(notice, "誰にも仕事を渡さずに") {
		t.Errorf("止まった理由が出ていない: %q", notice)
	}
}

// 下位を持たない相手が何も送らないのは、ふつうの終わり方である。騒がない。
func TestSilentWorkerIsQuiet(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{sendTo("hand", "やって"), {Type: provider.EventDone}}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead 作って", f.emit); err != nil {
		t.Fatal(err)
	}
	for _, e := range f.events {
		if e.Type == EvtNotice && strings.Contains(e.Text, "誰にも仕事を渡さずに") {
			t.Errorf("部下の手番に上司向けの知らせが出ている: %q", e.Text)
		}
	}
}

// チケットを触った道具は、一覧が古くなったことを流す。流さないと、手番が
// 回っている間ずっと古い一覧が画面に出たままになる (#189542)。
func TestTicketToolsAnnounceChange(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			return []provider.Event{callTool("create_ticket",
				map[string]any{"title": "仕事", "body": "やること"})}
		}
		if self == "lead" && nth == 1 {
			return []provider.Event{callTool("list_tickets", map[string]any{})}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}

	var changed []string
	for _, e := range f.events {
		if e.Type == EvtChanged {
			changed = append(changed, e.Text)
		}
	}
	// 起票は 1 回。読むだけの list_tickets では流れない。
	if len(changed) != 1 || changed[0] != "tickets" {
		t.Errorf("流れた知らせ = %v, want [tickets]", changed)
	}
	// 中身は載せない。載せると同じものを 2 つの経路で組み立てることになる。
	for _, e := range f.events {
		if e.Type == EvtChanged && (e.Result != "" || e.Args != nil) {
			t.Error("知らせに中身が載っている")
		}
	}
}

// 失敗した道具では流さない。何も変わっていない。
func TestFailedTicketToolIsNotAnnounced(t *testing.T) {
	f, sess := teamFixture(t, byMember(func(self string, nth int) []provider.Event {
		if self == "lead" && nth == 0 {
			// 番号の無い更新は断られる。
			return []provider.Event{callTool("update_ticket", map[string]any{})}
		}
		return done()
	}))
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatal(err)
	}
	for _, e := range f.events {
		if e.Type == EvtChanged {
			t.Errorf("失敗したのに知らせが流れている: %v", e.Text)
		}
	}
}
