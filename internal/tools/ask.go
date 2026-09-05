package tools

import (
	"context"
	"fmt"
	"strings"
)

// 選択肢の数の上限。多すぎると画面に収まらず、モデルも選ばずに書き足す。
const maxChoices = 6

type askUserTool struct{}

func (t *askUserTool) Name() string { return "ask_user" }
func (t *askUserTool) Description() string {
	return "利用者に問い、答えが返るまで待つ。答えによって作るものが変わり、" +
		"かつ自分では決められないことだけに使う。調べれば分かること、" +
		"妥当な既定を選べることには使わない。本文で問いかけても誰も答えないので、" +
		"問う必要があるときは必ずこれを使う。"
}
func (t *askUserTool) Parameters() map[string]any {
	return schema(map[string]any{
		"question": strProp("問い。何を決めたいのかと、決まらないと何ができないのかを書く。"),
		"choices": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "選ばせたい候補。自由に答えてもらう場合は省略する。",
		},
	}, "question")
}

// 問うこと自体は何も変えない。ここで確認を挟むと、確認のための確認になる。
func (t *askUserTool) NeedsApproval() bool { return false }

func (t *askUserTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	q, err := argString(args, "question")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("question が空です")
	}
	if ec.Ask == nil {
		// 問えない場面でも止めない。答えが返らないことを伝えて、自分で
		// 決めさせる。ここで失敗にすると、ターンごと落ちる。
		return "いまは利用者へ問えません。妥当な前提を自分で選んで進め、選んだ前提を答えに書き添えてください。", nil
	}
	return ec.Ask(ctx, q, argStrings(args, "choices", maxChoices))
}

// argStrings は文字列の並びを取り出す。モデルは 1 件のときに文字列だけを
// 返すことがあるため、そちらも受ける。
func argStrings(args map[string]any, key string, max int) []string {
	var out []string
	switch v := args[key].(type) {
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	case string:
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}
