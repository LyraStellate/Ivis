package websearch

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// duckduckgo は鍵の要らない取得元。HTML を読んで結果を取り出す。
//
// 先方の都合で形が変われば壊れる。それでも既定に据えるのは、何も登録せずに
// すぐ使えることのほうが、最初の一歩として重いからである。壊れたときは設定で
// 鍵のある取得元へ移せる。
type duckduckgo struct{ http *http.Client }

func (d *duckduckgo) Name() string { return "duckduckgo" }

// 結果は <a class="result__a" href="...">見出し</a> と、それに続く
// <a class="result__snippet">要約</a> の組で並ぶ。
var (
	ddgLink    = regexp.MustCompile(`(?s)<a[^>]+class="[^"]*result__a[^"]*"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippet = regexp.MustCompile(`(?s)<a[^>]+class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	htmlTag    = regexp.MustCompile(`<[^>]*>`)
)

func (d *duckduckgo) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	limit = clampLimit(limit)

	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// 既定の Go の名乗りでは断られる。
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Ivis/1.0)")

	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w (duckduckgo): %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, classify("duckduckgo", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}

	links := ddgLink.FindAllStringSubmatch(string(body), limit)
	snippets := ddgSnippet.FindAllStringSubmatch(string(body), limit)

	out := make([]Result, 0, len(links))
	for i, m := range links {
		r := Result{Title: plain(m[2]), URL: unwrap(m[1])}
		if i < len(snippets) {
			r.Snippet = plain(snippets[i][1])
		}
		if r.URL != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

// unwrap は中継の URL から本来の宛先を取り出す。そのまま返すと、モデルは
// 中継の URL を取得しに行き、本文が取れない。
func unwrap(raw string) string {
	raw = html.UnescapeString(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if dest := u.Query().Get("uddg"); dest != "" {
		return dest
	}
	return raw
}

// plain は取り出した断片からタグと実体参照を落とす。
func plain(s string) string {
	s = htmlTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
