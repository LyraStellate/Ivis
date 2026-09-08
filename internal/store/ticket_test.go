package store

import (
	"context"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

// 連番は会話ごとに 1 から振る。24 桁の識別子は会話で読み上げられない。
func TestTicketNumbersAreePerSession(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	a, _ := st.CreateTeamSession(ctx, "boss", "A")
	b, _ := st.CreateTeamSession(ctx, "boss", "B")

	for _, want := range []int{1, 2, 3} {
		tk := &Ticket{SessionID: a.ID, Title: "仕事"}
		if err := st.CreateTicket(ctx, tk); err != nil {
			t.Fatal(err)
		}
		if tk.Number != want {
			t.Errorf("番号 = %d, want %d", tk.Number, want)
		}
	}
	tk := &Ticket{SessionID: b.ID, Title: "別の会話"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if tk.Number != 1 {
		t.Errorf("別の会話の番号 = %d, want 1", tk.Number)
	}
}

func TestCreateTicketRejectsBadInput(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")

	if err := st.CreateTicket(ctx, &Ticket{SessionID: sess.ID, Title: "  "}); err == nil {
		t.Error("題が空でも通ってしまう")
	}
	if err := st.CreateTicket(ctx, &Ticket{SessionID: sess.ID, Title: "x", Status: "架空"}); err == nil {
		t.Error("知らない状態が通ってしまう")
	}
	if err := st.CreateTicket(ctx, &Ticket{SessionID: sess.ID, Title: "x", Priority: "最強"}); err == nil {
		t.Error("知らない優先度が通ってしまう")
	}
	// 省略したときの既定。
	tk := &Ticket{SessionID: sess.ID, Title: "既定"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if tk.Status != StatusNew || tk.Priority != PriorityNormal {
		t.Errorf("既定 = %s / %s", tk.Status, tk.Priority)
	}
}

// 状態と担当が動いたら注記が 1 行残る。履歴の表を別に持たないので、
// ここに残らなければ経過はどこにも無い。
func TestStatusChangeLeavesNote(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")
	tk := &Ticket{SessionID: sess.ID, Title: "仕事"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatal(err)
	}

	got, err := st.UpdateTicket(ctx, sess.ID, tk.Number, TicketPatch{
		Status: ptr(StatusInProgress), Assignee: ptr("hand")}, "boss")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Notes) != 2 {
		t.Fatalf("注記 = %d 件, want 2\n%+v", len(got.Notes), got.Notes)
	}
	if !strings.Contains(got.Notes[0].Body, StatusNew) ||
		!strings.Contains(got.Notes[0].Body, StatusInProgress) {
		t.Errorf("状態の注記が読めない: %q", got.Notes[0].Body)
	}
	if !got.Notes[0].Auto {
		t.Error("自動の注記に印が付いていない")
	}

	// 何も変えずに注記だけ足せる。
	if err := st.AddNote(ctx, sess.ID, tk.Number, "hand", "調べた結果", false); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTicket(ctx, sess.ID, tk.Number)
	last := got.Notes[len(got.Notes)-1]
	if last.Body != "調べた結果" || last.Auto {
		t.Errorf("手で書いた注記が残っていない: %+v", last)
	}

	// 同じ値で更新しても注記は増えない。
	before := len(got.Notes)
	got, _ = st.UpdateTicket(ctx, sess.ID, tk.Number, TicketPatch{Status: ptr(StatusInProgress)}, "boss")
	if len(got.Notes) != before {
		t.Error("変わっていないのに注記が増えている")
	}
}

// 既定の一覧は終了を除く。終わった仕事が並ぶと、残っているものが埋もれる。
func TestListTicketsFilters(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")
	mk := func(title, assignee, status string) {
		if err := st.CreateTicket(ctx, &Ticket{SessionID: sess.ID, Title: title,
			Assignee: assignee, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	mk("残り1", "hand", StatusNew)
	mk("残り2", "scout", StatusInProgress)
	mk("済み", "hand", StatusClosed)

	list, _ := st.ListTickets(ctx, sess.ID, TicketFilter{})
	if len(list) != 2 {
		t.Errorf("既定の件数 = %d, want 2 (終了を除く)", len(list))
	}
	list, _ = st.ListTickets(ctx, sess.ID, TicketFilter{IncludeClosed: true})
	if len(list) != 3 {
		t.Errorf("終了込みの件数 = %d, want 3", len(list))
	}
	list, _ = st.ListTickets(ctx, sess.ID, TicketFilter{Assignee: "hand"})
	if len(list) != 1 || list[0].Title != "残り1" {
		t.Errorf("担当で絞れていない: %v", list)
	}
	list, _ = st.ListTickets(ctx, sess.ID, TicketFilter{Status: StatusClosed})
	if len(list) != 1 || list[0].Title != "済み" {
		t.Errorf("状態で絞れていない: %v", list)
	}
	// 一覧は注記を含めない。全文を返すと、それだけでコンテキストが埋まる。
	for _, tk := range list {
		if tk.Notes != nil {
			t.Error("一覧に注記が入っている")
		}
	}
}

func TestUpdateUnknownTicket(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")
	if _, err := st.UpdateTicket(ctx, sess.ID, 99, TicketPatch{}, ""); err == nil {
		t.Error("存在しない番号の更新が通ってしまう")
	}
}

// 会話を消せばチケットと注記も消える。仕事の記録はその会話のものである。
func TestDeleteSessionRemovesTickets(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")
	tk := &Ticket{SessionID: sess.ID, Title: "仕事"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if err := st.AddNote(ctx, sess.ID, tk.Number, "boss", "覚え書き", false); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListTickets(ctx, sess.ID, TicketFilter{IncludeClosed: true}); len(list) != 0 {
		t.Errorf("チケットが残っている: %d 件", len(list))
	}
	if notes, _ := st.TicketNotes(ctx, sess.ID, tk.Number); len(notes) != 0 {
		t.Errorf("注記が残っている: %d 件", len(notes))
	}
}

// /clear は履歴と一緒にチケットも消す。発言を消してチケットだけ残すと、
// 何の話か分からない仕事の一覧が残る (#189542)。
func TestClearMessagesRemovesTickets(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")

	put(t, st, &Message{SessionID: sess.ID, Role: "user", Content: "やって", ToAgentID: "boss"})
	tk := &Ticket{SessionID: sess.ID, Title: "仕事"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if err := st.AddNote(ctx, sess.ID, tk.Number, "boss", "覚え書き", false); err != nil {
		t.Fatal(err)
	}

	got, err := st.ClearMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages != 1 || got.Tickets != 1 {
		t.Errorf("消した件数 = %+v, want {1 1}", got)
	}
	if list, _ := st.ListTickets(ctx, sess.ID, TicketFilter{IncludeClosed: true}); len(list) != 0 {
		t.Errorf("チケットが残っている: %d 件", len(list))
	}
	if notes, _ := st.TicketNotes(ctx, sess.ID, tk.Number); len(notes) != 0 {
		t.Errorf("注記が残っている: %d 件", len(notes))
	}
	// 消したあとに起票すると、番号は 1 から振り直される。
	next := &Ticket{SessionID: sess.ID, Title: "次"}
	if err := st.CreateTicket(ctx, next); err != nil {
		t.Fatal(err)
	}
	if next.Number != 1 {
		t.Errorf("番号 = %d, want 1", next.Number)
	}
}

// 巻き戻すと、その地点より後に起票されたチケットが消える。前からあるものは
// 中身ごと残す — 途中まで戻すと、状態とその理由が食い違った記録になる。
func TestRewindRemovesLaterTickets(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")

	put(t, st, &Message{SessionID: sess.ID, Role: "user", Content: "1 回目", ToAgentID: "boss"})
	early := &Ticket{SessionID: sess.ID, Title: "前からある仕事"}
	if err := st.CreateTicket(ctx, early); err != nil {
		t.Fatal(err)
	}
	from := put(t, st, &Message{SessionID: sess.ID, Role: "user", Content: "2 回目", ToAgentID: "boss"})
	late := &Ticket{SessionID: sess.ID, Title: "あとから出た仕事"}
	if err := st.CreateTicket(ctx, late); err != nil {
		t.Fatal(err)
	}
	if err := st.AddNote(ctx, sess.ID, late.Number, "boss", "経過", false); err != nil {
		t.Fatal(err)
	}

	got, text, err := st.Rewind(ctx, sess.ID, from.ID)
	if err != nil {
		t.Fatal(err)
	}
	if text != "2 回目" {
		t.Errorf("戻す本文 = %q", text)
	}
	if got.Tickets != 1 {
		t.Errorf("消したチケット = %d, want 1", got.Tickets)
	}

	list, _ := st.ListTickets(ctx, sess.ID, TicketFilter{IncludeClosed: true})
	if len(list) != 1 || list[0].Title != "前からある仕事" {
		t.Fatalf("残ったチケット = %v", list)
	}
	if notes, _ := st.TicketNotes(ctx, sess.ID, late.Number); len(notes) != 0 {
		t.Errorf("消したチケットの注記が残っている: %d 件", len(notes))
	}
}

// 巻き戻しの地点より前からあるチケットは、状態も注記もそのまま残る。
func TestRewindKeepsEarlierTicketIntact(t *testing.T) {
	st := openTeamStore(t)
	ctx := context.Background()
	sess, _ := st.CreateTeamSession(ctx, "boss", "")

	put(t, st, &Message{SessionID: sess.ID, Role: "user", Content: "1 回目", ToAgentID: "boss"})
	tk := &Ticket{SessionID: sess.ID, Title: "続いている仕事"}
	if err := st.CreateTicket(ctx, tk); err != nil {
		t.Fatal(err)
	}
	from := put(t, st, &Message{SessionID: sess.ID, Role: "user", Content: "2 回目", ToAgentID: "boss"})
	if _, err := st.UpdateTicket(ctx, sess.ID, tk.Number,
		TicketPatch{Status: ptr(StatusInProgress)}, "boss"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.Rewind(ctx, sess.ID, from.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTicket(ctx, sess.ID, tk.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInProgress {
		t.Errorf("状態 = %q, want %q (中身は戻さない)", got.Status, StatusInProgress)
	}
	if len(got.Notes) != 1 {
		t.Errorf("注記 = %d 件, want 1 (中身は戻さない)", len(got.Notes))
	}
}
