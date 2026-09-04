package tools

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/websearch"
)

const (
	// 取得した本文の上限。1 ページで文脈を使い切らせない。
	maxFetchBytes = 40 << 10
	// 転送量の上限。切る前に読み込む量そのものを抑える。
	maxFetchTransfer = 4 << 20
)

type webSearchTool struct{}

func (t *webSearchTool) Name() string { return "web_search" }
func (t *webSearchTool) Description() string {
	return "Web を検索し、見出し・URL・要約の並びを返す。学習時点より新しいことや、" +
		"手元にない情報が必要なときに使う。中身を読むには続けて fetch_url を呼ぶこと。"
}
func (t *webSearchTool) Parameters() map[string]any {
	return schema(map[string]any{
		"query": strProp("検索する語句。"),
		"limit": map[string]any{
			"type":        "integer",
			"description": "取得する件数。既定は 5、上限は 20。",
		},
	}, "query")
}
func (t *webSearchTool) NeedsApproval() bool { return false }

func (t *webSearchTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	query, err := argString(args, "query")
	if err != nil {
		return "", err
	}
	if ec.Search == nil {
		return "", fmt.Errorf("検索の取得元が設定されていません")
	}

	results, err := ec.Search.Search(ctx, query, argInt(args, "limit"))
	if err != nil {
		// 失敗の種類をそのまま文にして返す。「検索できません」だけでは、
		// 待てばよいのか鍵が要るのかをモデルが判断できない。
		switch {
		case errors.Is(err, websearch.ErrNeedsKey):
			return "", fmt.Errorf("%v。設定で検索の API キーを入れるか、取得元を変えてください", err)
		case errors.Is(err, websearch.ErrRateLimited):
			return "", err
		}
		return "", err
	}
	if len(results) == 0 {
		return fmt.Sprintf("%q に対する結果はありませんでした。語句を変えてみてください。", query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s の検索結果 (%d 件):\n", ec.Search.Name(), len(results))
	for i, r := range results {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", oneLine(r.Snippet, 300))
		}
	}
	return b.String(), nil
}

type fetchURLTool struct{}

func (t *fetchURLTool) Name() string { return "fetch_url" }
func (t *fetchURLTool) Description() string {
	return "URL の中身を取得し、本文をテキストにして返す。検索結果は見出しと要約だけなので、" +
		"読む必要があるものはこれで取ること。JavaScript が要るページは取れない。"
}
func (t *fetchURLTool) Parameters() map[string]any {
	return schema(map[string]any{
		"url": strProp("取得する URL。http か https で始まること。"),
	}, "url")
}
func (t *fetchURLTool) NeedsApproval() bool { return false }

func (t *fetchURLTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	raw, err := argString(args, "url")
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "", fmt.Errorf("URL は http か https で始めてください: %s", raw)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Ivis/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("取得できませんでした: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("取得できませんでした (status %d): %s", resp.StatusCode, raw)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchTransfer))
	if err != nil {
		return "", err
	}
	text := string(body)
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		text = extractText(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("本文を取り出せませんでした: %s", raw)
	}
	// 切ったことは必ず示す。分からないと、モデルは途中で終わる文書を
	// 完全なものとして扱う。
	return raw + "\n\n" + truncate(text, maxFetchBytes), nil
}

var (
	// Go の正規表現は後方参照を持たないため、落とす要素は 1 つずつ並べる。
	dropBlock = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<noscript[^>]*>.*?</noscript>|<svg[^>]*>.*?</svg>|<head[^>]*>.*?</head>`)
	breakTag  = regexp.MustCompile(`(?i)</(p|div|li|tr|h[1-6]|section|article|br)>`)
	anyTag    = regexp.MustCompile(`<[^>]*>`)
	manyLines = regexp.MustCompile(`\n{3,}`)
)

// extractText は HTML から本文だけを取り出す。飾りを落とし、段落の区切りを
// 改行として残す。取り出しに失敗しても生のまま返すほうが、何も返らないより
// 役に立つ。
func extractText(s string) string {
	s = dropBlock.ReplaceAllString(s, " ")
	s = breakTag.ReplaceAllString(s, "\n")
	s = anyTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)

	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if t := strings.Join(strings.Fields(line), " "); t != "" {
			b.WriteString(t)
			b.WriteString("\n")
		}
	}
	return manyLines.ReplaceAllString(b.String(), "\n\n")
}
