// Package websearch は Web 検索の取得元を提供する。
//
// 取得元をひとつのインターフェースにまとめるのは、鍵の有無で切り替わる実装を
// 同じ形で扱い、試験でモックを差し込めるようにするためである (#470913)。
package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Result は検索結果 1 件。
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Searcher は検索の取得元。
type Searcher interface {
	// Name は表示用の名前。どこから取ったのかを結果に添えるために使う。
	Name() string
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

// ErrNeedsKey は取得元が鍵を要求していること。利用者が次にすべきことは
// 「設定に鍵を入れる」であり、通信の失敗と混ぜてはならない。
var ErrNeedsKey = errors.New("この取得元には API キーが必要です")

// ErrUnavailable は取得元に届かない、または断られたこと。
var ErrUnavailable = errors.New("検索の取得元に接続できません")

// ErrRateLimited は連打などで一時的に断られたこと。待てば直る。
var ErrRateLimited = errors.New("検索の取得元に断られました。しばらく待ってから試してください")

// Backends は設定で選べる取得元の名前。先頭が既定。
var Backends = []string{"duckduckgo", "brave", "tavily"}

// New は名前と鍵から取得元を作る。知らない名前は既定に落とす。設定の
// 打ち間違いで検索が丸ごと使えなくなるより、既定で動くほうがよい。
func New(name, apiKey string) Searcher {
	c := &http.Client{Timeout: 20 * time.Second}
	switch name {
	case "brave":
		return &brave{key: apiKey, http: c}
	case "tavily":
		return &tavily{key: apiKey, http: c}
	default:
		return &duckduckgo{http: c}
	}
}

// clampLimit は件数を現実的な範囲に収める。多すぎる結果はそれだけで
// 文脈を食い、モデルは上から数件しか見ない。
func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 5
	case n > 20:
		return 20
	}
	return n
}

// classify は HTTP の状態を、利用者が次に何をすべきか分かる型へ落とす。
func classify(name string, status int) error {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("%w (%s)", ErrNeedsKey, name)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w (%s)", ErrRateLimited, name)
	}
	return fmt.Errorf("%w (%s): status %d", ErrUnavailable, name, status)
}
