package tools

import (
	"context"
	"os"
	"strings"
	"testing"
)

// 取得は先方のページ次第で結果が変わる。本文が抜けているかは実際に当てないと
// 分からないので、明示したときだけ走らせる。
//
//	IVIS_LIVE_FETCH=1 go test ./internal/tools/ -run Live -v
func TestLiveFetchURL(t *testing.T) {
	if os.Getenv("IVIS_LIVE_FETCH") == "" {
		t.Skip("IVIS_LIVE_FETCH を設定すると実際に取得します")
	}
	ec := newCtx(t)
	out, err := (&fetchURLTool{}).Execute(context.Background(), ec,
		map[string]any{"url": "https://go.dev/doc/articles/wiki/"})
	if err != nil {
		t.Fatalf("取得できません: %v", err)
	}
	t.Logf("取得した長さ: %d バイト", len(out))
	t.Logf("先頭:\n%s", firstLines(out, 12))

	// タグの形をした文字列は、記事が載せている実体参照が戻ったものでもある
	// (この記事は HTML の例を含む)。落ちているべきなのは、実体参照では
	// 現れようのない部分 — script の中身と、文書そのものの骨組みである。
	for _, bad := range []string{"<!DOCTYPE", "<script", "</html>"} {
		if strings.Contains(out, bad) {
			t.Errorf("飾りが残っています: %q", bad)
		}
	}
	if !strings.Contains(out, "Writing Web Applications") {
		t.Error("本文が取れていません")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
