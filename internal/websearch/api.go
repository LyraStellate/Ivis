package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// brave は Brave Search API を使う取得元。
type brave struct {
	key  string
	http *http.Client
}

func (b *brave) Name() string { return "brave" }

func (b *brave) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if b.key == "" {
		return nil, fmt.Errorf("%w (brave)", ErrNeedsKey)
	}
	limit = clampLimit(limit)

	u := "https://api.search.brave.com/res/v1/web/search?" + url.Values{
		"q":     {query},
		"count": {strconv.Itoa(limit)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.key)

	body, err := send(b.http, req, "brave")
	if err != nil {
		return nil, err
	}
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	res := make([]Result, 0, len(out.Web.Results))
	for _, r := range out.Web.Results {
		res = append(res, Result{Title: plain(r.Title), URL: r.URL, Snippet: plain(r.Description)})
	}
	return res, nil
}

// tavily は Tavily の検索 API を使う取得元。
type tavily struct {
	key  string
	http *http.Client
}

func (t *tavily) Name() string { return "tavily" }

func (t *tavily) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if t.key == "" {
		return nil, fmt.Errorf("%w (tavily)", ErrNeedsKey)
	}
	limit = clampLimit(limit)

	payload, err := json.Marshal(map[string]any{
		"query":       query,
		"max_results": limit,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.tavily.com/search", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.key)

	body, err := send(t.http, req, "tavily")
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	res := make([]Result, 0, len(out.Results))
	for _, r := range out.Results {
		res = append(res, Result{Title: plain(r.Title), URL: r.URL, Snippet: plain(r.Content)})
	}
	return res, nil
}

// send は要求を投げ、失敗を種類の分かる形に落として本文を返す。
func send(c *http.Client, req *http.Request, name string) ([]byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %v", ErrUnavailable, name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, classify(name, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
