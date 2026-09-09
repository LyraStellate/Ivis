package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/team"
)

// コンテキストの圧縮 (#486237)。
//
// ローカルモデルのコンテキストは狭く、道具を往復すると数ターンで埋まる。
// 埋まると提供元は古い側から黙って捨てるので、指示文ごと失われる。減らす手が
// 履歴の全削除しか無いと、長い作業の途中では使えない。
//
// ここでやるのは、それまでのやり取りを 1 件の要約にまとめ、モデルへ渡す入力を
// その要約から後ろだけに切り替えることである。元の発言は消さない。
//
// 直近のやり取りを機械的に残す形も考えたが、要約は会話の末尾に足す 1 件で
// あって、そこより前に置く場所が無い。連番の途中へ挿し込むには番号を振り直す
// ことになり、巻き戻しの起点も動く。残す範囲は指示文 (KEEP の「直近5往復」)
// でモデルに任せ、データの形は 1 本の並びのままにする。

// DefaultCompactPrompt は何を残し、何をまとめ、何を捨てるかの既定の指示。
// /compact に引数があれば、これを足すのではなく置き換える。
const DefaultCompactPrompt = `KEEP:
- 今の目標と、完了の条件
- 変更したファイルと、その理由
- 重要なコード上の判断と、ボツにした案
- 未解決のバグ、失敗中のテスト、試したコマンド
- 直近5往復のやり取り
SUMMARIZE:
- 序盤の調査
- 終わったデバッグの過程
- 雑談的なやり取り
DROP:
- くり返しのテスト出力
- もう関係ない長いログ
- すでに捨てたアイデア`

// autoCompactAt は自動で圧縮を始める割合。
const autoCompactAt = 0.9

// StageCompacting は圧縮の最中であることを表す印。画面はこれを見て、待って
// いる時間に何をしているかを出す。
const StageCompacting = "compacting"

// summaryHeader は要約をモデルへ渡すときに添える見出し。何の文章なのかを
// 書いておかないと、モデルはこれを利用者の発言として読む。
const summaryHeader = "これまでのやり取りの要約です。ここに書かれていることは既に起きたこととして扱ってください。\n\n"

// ErrNothingToCompact はまとめる分が無いこと。前回の要約しか無い会話をもう
// 一度まとめても、要約の要約ができるだけで何も減らない。
var ErrNothingToCompact = errors.New("まとめる分がありません")

// CompactResult は圧縮の結果。
type CompactResult struct {
	// Summarized は要約にまとめた発言の数。
	Summarized int
	Summary    string
}

// Compact はこの会話のコンテキストを圧縮する。
//
// instructions が空なら DefaultCompactPrompt を使う。生成に失敗したときは
// 何も書かない。中途半端な要約で入力を切り替えるほうが、圧縮しないより悪い。
// progress は要約が伸びるたびに、そこまでにできた文字数で呼ばれる。nil なら
// 報告しない。何も届かない時間が数十秒続くので、進んでいることを外へ出せる
// 口をここに開けてある。
func (e *Engine) Compact(ctx context.Context, sessionID, instructions string, progress func(chars int)) (*CompactResult, error) {
	sess, err := e.Store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	ag, err := e.summarizer(sess)
	if err != nil {
		return nil, err
	}

	// 対象は前回の要約以降。要約が要約を含む形になるので、古い話ほど圧縮が
	// 重なって短くなる。
	target, err := e.Store.ConversationMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !worthCompacting(target) {
		return nil, ErrNothingToCompact
	}

	prompt := strings.TrimSpace(instructions)
	if prompt == "" {
		prompt = DefaultCompactPrompt
	}
	summary, err := e.oneShot(ctx, ag, prompt, transcript(target), progress)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(summary) == "" {
		return nil, errors.New("要約が空でした")
	}

	if err := e.Store.AppendMessage(ctx, &store.Message{
		SessionID: sessionID,
		Role:      store.RoleSummary,
		Content:   summary,
		AgentID:   ag.ID,
		Model:     ag.Model,
	}); err != nil {
		return nil, fmt.Errorf("要約を保存できませんでした: %w", err)
	}

	// 実測値はここでは分からない。90% のまま出し続けると圧縮が効かなかった
	// ように見えるので、不明へ戻す。正しい値は次のターンの実測で入る。
	if err := e.Store.SetContextUsage(ctx, sessionID, 0, 0); err != nil {
		return nil, err
	}
	return &CompactResult{Summarized: len(target), Summary: summary}, nil
}

// summarizer は要約を書くエージェントを選ぶ。
//
// 直列の会話ではその会話の答え手である。チームには答え手が居ないので、名簿の
// 先頭を採る。名簿は Tier 順・同じ Tier では ID 順に整列済みなので、必ず 1 人に
// 定まる。誰が引き金を引いても結果が同じになるよう、手番の主 (そのとき溢れた
// 本人) は使わない — 使うと、同じ会話の要約の質が回ごとに揺れる理由を説明
// できなくなる (#640275)。
func (e *Engine) summarizer(sess *store.Session) (*agent.Agent, error) {
	if sess.Kind == config.KindTeam {
		roster := team.Load(e.Agents, e.TeamAgents, sess)
		if len(roster.Members) == 0 {
			return nil, fmt.Errorf("この会話にはメンバーが 1 人も居ないので、まとめられません")
		}
		return roster.Members[0].Agent, nil
	}
	ag, ok := e.Agents.Get(sess.AgentID)
	if !ok {
		return nil, fmt.Errorf("エージェント %q の定義が見つかりません", sess.AgentID)
	}
	return ag, nil
}

// worthCompacting はまとめる余地があるかを返す。
//
// 前回の要約しか無い会話をもう一度まとめても、要約の要約ができるだけで何も
// 減らない。要約以外の発言が 1 件も無ければ断る。
func worthCompacting(history []*store.Message) bool {
	for _, m := range history {
		if m.Role != store.RoleSummary {
			return true
		}
	}
	return false
}

// transcript はまとめる対象を 1 つの文章にする。役割が読み取れる形にして
// おかないと、モデルは誰の発言なのかを取り違える。
func transcript(msgs []*store.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		var who string
		switch m.Role {
		case provider.RoleUser:
			who = "利用者"
		case provider.RoleAssistant:
			who = "アシスタント"
		case provider.RoleTool:
			who = "道具 (" + m.ToolName + ")"
		case store.RoleSummary:
			who = "これまでの要約"
		case store.RoleTeam:
			// 誰から誰へのメッセージかを書かないと、まとめた文が「誰かが
			// 何かを言った」の羅列になる (#640275)。
			who = m.AgentID + " → " + m.ToAgentID
			if m.Decision != "" {
				who += " (" + m.Decision + ")"
			}
		case "delegate":
			who = "委譲 (" + m.AgentID + ")"
		default:
			who = m.Role
		}
		text := strings.TrimSpace(m.Content)
		if text == "" {
			// 本文の無い発言は、道具を呼ぶためだけの生成だった。呼んだ事実は
			// 次の道具の結果に現れるので、ここでは落とす。
			continue
		}
		b.WriteString("## " + who + "\n")
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

// oneShot は道具も推論も使わない 1 回の生成を行い、本文を返す。
//
// 要約に道具は要らず、推論の過程は捨てる値である。どちらもコンテキストと
// 時間を使うだけになる。
func (e *Engine) oneShot(ctx context.Context, ag *agent.Agent, sys, user string, progress func(chars int)) (string, error) {
	stream, err := e.Provider.Chat(ctx, provider.Request{
		Model: ag.Model,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: sys},
			{Role: provider.RoleUser, Content: user},
		},
		Options: e.options(ctx, ag),
		Think:   false,
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	// 報告は一定量ごとにまとめる。1 片ごとに流すと、要約の間じゅう画面が
	// 数字の更新だけで埋まる。
	const step = 120
	last := 0
	for ev := range stream {
		switch ev.Type {
		case provider.EventDelta:
			b.WriteString(ev.Text)
			if progress != nil && b.Len()-last >= step {
				last = b.Len()
				progress(len([]rune(b.String())))
			}
		case provider.EventError:
			return "", ev.Err
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return strings.TrimSpace(b.String()), nil
}

// maybeCompact は使ったコンテキストが上限に近ければ圧縮する。
//
// ターンの終わりに 1 度だけ呼ぶ。走っている最中に入力を組み替えることは
// できないし、次のターンを始める前に済ませておけば、待たされる場所が
// 1 か所にまとまる。
//
// 圧縮しても割合が下がらないことはある (直近のやり取りだけで埋まっている
// 場合)。ここで繰り返すと、生成が終わるたびに生成が走り続けるので、1 ターン
// につき 1 度しか試さない。
func (e *Engine) maybeCompact(ctx context.Context, sessionID string, emit Emit) {
	sess, err := e.Store.GetSession(ctx, sessionID)
	if err != nil || sess.ContextLimit <= 0 {
		return
	}
	if float64(sess.ContextTokens) < autoCompactAt*float64(sess.ContextLimit) {
		return
	}

	emit(Event{Type: EvtNotice, Text: fmt.Sprintf(
		"コンテキストが上限の %d%% に達したため、これまでのやり取りをまとめています。",
		int(autoCompactAt*100))})

	emit(Event{Type: EvtStage, Stage: StageCompacting})
	defer emit(Event{Type: EvtStage})

	res, err := e.Compact(ctx, sessionID, "", func(chars int) {
		emit(Event{Type: EvtStage, Stage: StageCompacting, Done: chars})
	})
	if err != nil {
		if errors.Is(err, ErrNothingToCompact) {
			// まとめる分が無い。ここで騒いでも、利用者に打てる手が無い。
			return
		}
		emit(Event{Type: EvtNotice, Text: "まとめられませんでした: " + err.Error()})
		return
	}
	emit(Event{Type: EvtNotice, Text: fmt.Sprintf(
		"%d 件をまとめました。元のやり取りは消えていません。", res.Summarized)})
}
