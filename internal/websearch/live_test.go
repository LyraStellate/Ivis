package websearch

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 鍵の要らない取得元は先方の HTML を読むため、こちらの都合と無関係に壊れる。
// 壊れたことは実際に当てないと分からないので、明示したときだけ走らせる。
//
//	IVIS_LIVE_SEARCH=1 go test ./internal/websearch/ -run Live -v
func TestLiveDuckDuckGo(t *testing.T) {
	if os.Getenv("IVIS_LIVE_SEARCH") == "" {
		t.Skip("IVIS_LIVE_SEARCH を設定すると実際に検索します")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := New("duckduckgo", "").Search(ctx, "golang http server", 5)
	if err != nil {
		t.Fatalf("検索できません: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("結果が 0 件です。先方の作りが変わった可能性があります")
	}
	for i, r := range results {
		t.Logf("%d. %s\n   %s\n   %s", i+1, r.Title, r.URL, r.Snippet)
		if r.Title == "" || !strings.HasPrefix(r.URL, "http") {
			t.Errorf("%d 件目が取り出せていません: %+v", i+1, r)
		}
		// 中継の URL のまま返すと、モデルはそこを取得しに行き本文が取れない。
		if strings.Contains(r.URL, "duckduckgo.com/l/") {
			t.Errorf("%d 件目が中継の URL のままです: %s", i+1, r.URL)
		}
	}
}
