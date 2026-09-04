package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LyraStellate/Ivis/internal/websearch"
)

type fakeSearch struct {
	results []websearch.Result
	err     error
}

func (f *fakeSearch) Name() string { return "fake" }
func (f *fakeSearch) Search(ctx context.Context, q string, n int) ([]websearch.Result, error) {
	return f.results, f.err
}

func TestWebSearchFormatsResults(t *testing.T) {
	ec := newCtx(t)
	ec.Search = &fakeSearch{results: []websearch.Result{
		{Title: "はじめの一歩", URL: "https://example.com/a", Snippet: "要約 A"},
		{Title: "つぎの一歩", URL: "https://example.com/b"},
	}}

	out, err := (&webSearchTool{}).Execute(context.Background(), ec, map[string]any{"query": "歩"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fake", "はじめの一歩", "https://example.com/a", "要約 A", "つぎの一歩"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出ていません:\n%s", want, out)
		}
	}
}

// 失敗の種類は保つ。「検索できません」だけでは、待てばよいのか鍵が要るのか
// モデルが判断できない。
func TestWebSearchKeepsFailureKind(t *testing.T) {
	ec := newCtx(t)
	ec.Search = &fakeSearch{err: websearch.ErrNeedsKey}

	_, err := (&webSearchTool{}).Execute(context.Background(), ec, map[string]any{"query": "x"})
	if err == nil {
		t.Fatal("失敗が伝わっていません")
	}
	if !strings.Contains(err.Error(), "API キー") {
		t.Errorf("次にすべきことが示されていません: %v", err)
	}
}

func TestWebSearchNeedsBackend(t *testing.T) {
	ec := newCtx(t)
	if _, err := (&webSearchTool{}).Execute(context.Background(), ec,
		map[string]any{"query": "x"}); err == nil {
		t.Fatal("取得元が無いのに成功しています")
	}
}

func TestWebSearchEmptyResult(t *testing.T) {
	ec := newCtx(t)
	ec.Search = &fakeSearch{}
	out, err := (&webSearchTool{}).Execute(context.Background(), ec, map[string]any{"query": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "結果はありません") {
		t.Errorf("空の結果が伝わっていません: %q", out)
	}
}

// 飾りを落とし、段落の区切りは改行として残す。
func TestExtractText(t *testing.T) {
	got := extractText(`<html><head><title>捨てる</title></head><body>
		<script>var x = "捨てる";</script>
		<style>.a{color:red}</style>
		<h1>見出し</h1><p>本文の &amp; 一段落</p><p>次の段落</p>
		</body></html>`)

	for _, bad := range []string{"var x", "color:red", "<p>"} {
		if strings.Contains(got, bad) {
			t.Errorf("飾りが残っています (%q):\n%s", bad, got)
		}
	}
	for _, want := range []string{"見出し", "本文の & 一段落", "次の段落"} {
		if !strings.Contains(got, want) {
			t.Errorf("本文が落ちています (%q):\n%s", want, got)
		}
	}
	// 段落が 1 行に潰れると、どこで切れているのか読めなくなる。
	if !strings.Contains(got, "一段落\n次の段落") {
		t.Errorf("段落の区切りが残っていません:\n%q", got)
	}
}

func TestFetchURLRejectsNonHTTP(t *testing.T) {
	ec := newCtx(t)
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com", "example.com"} {
		if _, err := (&fetchURLTool{}).Execute(context.Background(), ec,
			map[string]any{"url": u}); err == nil {
			t.Errorf("%q が通ってしまいました", u)
		}
	}
}

func TestWebSearchWrapsRateLimit(t *testing.T) {
	ec := newCtx(t)
	ec.Search = &fakeSearch{err: websearch.ErrRateLimited}
	_, err := (&webSearchTool{}).Execute(context.Background(), ec, map[string]any{"query": "x"})
	if !errors.Is(err, websearch.ErrRateLimited) {
		t.Errorf("種類が失われています: %v", err)
	}
}
