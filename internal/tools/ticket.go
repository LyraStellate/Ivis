package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/LyraStellate/Ivis/internal/store"
)

// チケットのツールは、チームセッションの唯一の共有状態を読み書きする。
//
// メンバーは自分宛てのやり取りしか読めないので、誰が何を持ち、どこまで
// 進んでいるかは会話ではなくここにしか無い。だからこれは「あると便利な
// 一覧」ではなく、チームが互いの状況を知る唯一の手段である (#189542)。
//
// どれも承認を求めない。会話の中だけで完結し、注記が残るので取り消せる。
// 1 回の状態変更ごとに承認を挟めば、チームは 1 歩ごとに止まる。承認は、
// 会話の外へ出る操作 (ファイル・コマンド・ネットワーク) のためにある。

// ticketBase はチケットのツールに共通の下ごしらえ。
type ticketBase struct{ teamOnly }

func (ticketBase) NeedsApproval() bool { return false }

// tickets は書き込み先を取り出す。チーム以外では nil になる。
func ticketsOf(ec *ExecContext) (*store.Store, string, error) {
	if ec.Tickets == nil || ec.Session == "" {
		return nil, "", fmt.Errorf("この会話にはチケットがありません")
	}
	return ec.Tickets, ec.Session, nil
}

// assigneeOK は担当が名簿に居るかを見る。居ない相手を担当にすると、その
// 仕事は誰の一覧にも出てこないまま残る。
func assigneeOK(ec *ExecContext, id string) error {
	if id == "" || ec.Team == nil {
		return nil
	}
	if _, ok := ec.Team.member(id); !ok {
		return fmt.Errorf("%q はこの会話に居ません。%s", id, ec.Team.roll())
	}
	return nil
}

type createTicketTool struct{ ticketBase }

func (t *createTicketTool) Name() string { return "create_ticket" }
func (t *createTicketTool) Description() string {
	return "仕事を 1 件起票する。チームの誰もが読める唯一の記録なので、受けた仕事はまずここに残すこと。"
}
func (t *createTicketTool) Parameters() map[string]any {
	return schema(map[string]any{
		"title":    strProp("題。一覧に出るので、何の仕事かが 1 行で分かるように書く。"),
		"body":     strProp("概要。何をすれば完了かを書く。"),
		"assignee": strProp("担当のメンバー ID。決まっていなければ省略する。"),
		"due":      strProp("期限。YYYY-MM-DD の日付で書く。"),
		"priority": strProp("優先度。" + strings.Join(store.Priorities, " / ") + " のいずれか。既定は中。"),
	}, "title", "body")
}

func (t *createTicketTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	st, sid, err := ticketsOf(ec)
	if err != nil {
		return "", err
	}
	title, err := argString(args, "title")
	if err != nil {
		return "", err
	}
	assignee := strings.TrimPrefix(argStringOpt(args, "assignee"), "@")
	if err := assigneeOK(ec, assignee); err != nil {
		return "", err
	}
	tk := &store.Ticket{
		SessionID: sid,
		Title:     title,
		Body:      argStringOpt(args, "body"),
		Assignee:  assignee,
		Due:       argStringOpt(args, "due"),
		Priority:  argStringOpt(args, "priority"),
		Author:    ec.AgentID,
	}
	if err := st.CreateTicket(ctx, tk); err != nil {
		return "", err
	}
	return fmt.Sprintf("#%d を起票しました (%s)。", tk.Number, tk.Title), nil
}

type updateTicketTool struct{ ticketBase }

func (t *updateTicketTool) Name() string { return "update_ticket" }
func (t *updateTicketTool) Description() string {
	return "チケットの項目を変える、または注記を足す。変えたい項目だけ渡せばよい。" +
		"判断したこと・試して駄目だったことは注記に残すこと。状態と担当の変更は自動で注記に残る。"
}
func (t *updateTicketTool) Parameters() map[string]any {
	return schema(map[string]any{
		"number":   map[string]any{"type": "integer", "description": "チケットの番号。"},
		"status":   strProp("状態。" + strings.Join(store.Statuses, " / ") + " のいずれか。"),
		"assignee": strProp("担当のメンバー ID。外すときは空文字を渡す。"),
		"priority": strProp("優先度。" + strings.Join(store.Priorities, " / ") + " のいずれか。"),
		"due":      strProp("期限。YYYY-MM-DD。"),
		"title":    strProp("題。"),
		"body":     strProp("概要。書き換えると前の内容は残らない。経過は note に書くこと。"),
		"note":     strProp("足す注記。消せないので、残す価値のあることだけを書く。"),
	}, "number")
}

func (t *updateTicketTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	st, sid, err := ticketsOf(ec)
	if err != nil {
		return "", err
	}
	num := argInt(args, "number")
	if num <= 0 {
		return "", fmt.Errorf("number にチケットの番号を指定してください")
	}

	var patch store.TicketPatch
	// 渡された項目だけを変える。空文字も意思なので、キーの有無で見分ける。
	if _, ok := args["title"]; ok {
		v := argStringOpt(args, "title")
		patch.Title = &v
	}
	if _, ok := args["body"]; ok {
		v := argStringOpt(args, "body")
		patch.Body = &v
	}
	if _, ok := args["assignee"]; ok {
		v := strings.TrimPrefix(argStringOpt(args, "assignee"), "@")
		if err := assigneeOK(ec, v); err != nil {
			return "", err
		}
		patch.Assignee = &v
	}
	if _, ok := args["due"]; ok {
		v := argStringOpt(args, "due")
		patch.Due = &v
	}
	if _, ok := args["status"]; ok {
		v := argStringOpt(args, "status")
		patch.Status = &v
	}
	if _, ok := args["priority"]; ok {
		v := argStringOpt(args, "priority")
		patch.Priority = &v
	}

	tk, err := st.UpdateTicket(ctx, sid, num, patch, ec.AgentID)
	if err != nil {
		return "", err
	}
	if note := strings.TrimSpace(argStringOpt(args, "note")); note != "" {
		if err := st.AddNote(ctx, sid, num, ec.AgentID, note, false); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("#%d を更新しました (%s / %s / 担当 %s)。",
		tk.Number, tk.Status, tk.Priority, orNobody(tk.Assignee)), nil
}

type getTicketTool struct{ ticketBase }

func (t *getTicketTool) Name() string { return "get_ticket" }
func (t *getTicketTool) Description() string {
	return "チケット 1 件を、概要と注記の全部まで読む。"
}
func (t *getTicketTool) Parameters() map[string]any {
	return schema(map[string]any{
		"number": map[string]any{"type": "integer", "description": "チケットの番号。"},
	}, "number")
}

func (t *getTicketTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	st, sid, err := ticketsOf(ec)
	if err != nil {
		return "", err
	}
	num := argInt(args, "number")
	if num <= 0 {
		return "", fmt.Errorf("number にチケットの番号を指定してください")
	}
	tk, err := st.GetTicket(ctx, sid, num)
	if err != nil {
		return "", err
	}
	return FormatTicket(tk), nil
}

type listTicketsTool struct{ ticketBase }

func (t *listTicketsTool) Name() string { return "list_tickets" }
func (t *listTicketsTool) Description() string {
	return "チケットの一覧を見る。ほかのメンバーが何を持ち、どこまで進んでいるかはここでしか分からない。" +
		"概要と注記は含まれないので、中身が要るなら get_ticket で 1 件を読む。"
}
func (t *listTicketsTool) Parameters() map[string]any {
	return schema(map[string]any{
		"assignee": strProp("担当で絞る。自分の分だけ見たいなら自分の ID。"),
		"status":   strProp("状態で絞る。" + strings.Join(store.Statuses, " / ") + " のいずれか。"),
		"include_closed": map[string]any{"type": "boolean",
			"description": "終了したものも含めるか。既定では含めない。"},
	})
}

func (t *listTicketsTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	st, sid, err := ticketsOf(ec)
	if err != nil {
		return "", err
	}
	f := store.TicketFilter{
		Assignee:      strings.TrimPrefix(argStringOpt(args, "assignee"), "@"),
		Status:        argStringOpt(args, "status"),
		IncludeClosed: argBool(args, "include_closed"),
	}
	list, err := st.ListTickets(ctx, sid, f)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "該当するチケットはありません。", nil
	}
	return FormatTicketList(list), nil
}

// FormatTicketList は一覧の 1 行表現。指示文へ載せる担当分にも使う。
func FormatTicketList(list []*store.Ticket) string {
	var b strings.Builder
	for _, t := range list {
		fmt.Fprintf(&b, "- #%d %s [%s/%s] 担当 %s", t.Number, t.Title, t.Status, t.Priority,
			orNobody(t.Assignee))
		if t.Due != "" {
			fmt.Fprintf(&b, " 期限 %s", t.Due)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatTicket は 1 件の全文。
func FormatTicket(t *store.Ticket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s\n", t.Number, t.Title)
	fmt.Fprintf(&b, "状態: %s / 優先度: %s / 担当: %s", t.Status, t.Priority, orNobody(t.Assignee))
	if t.Due != "" {
		fmt.Fprintf(&b, " / 期限: %s", t.Due)
	}
	fmt.Fprintf(&b, "\n起票: %s (%s)\n", orNobody(t.Author), t.CreatedAt.Format("2006-01-02 15:04"))
	if strings.TrimSpace(t.Body) != "" {
		b.WriteString("\n" + t.Body + "\n")
	}
	if len(t.Notes) > 0 {
		b.WriteString("\n注記:\n")
		for _, n := range t.Notes {
			mark := ""
			if n.Auto {
				mark = " (自動)"
			}
			fmt.Fprintf(&b, "- %s %s%s: %s\n", n.CreatedAt.Format("01-02 15:04"),
				orNobody(n.Author), mark, n.Body)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func orNobody(id string) string {
	if id == "" {
		return "(未割り当て)"
	}
	return id
}
