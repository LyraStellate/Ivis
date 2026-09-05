package discord

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitKeepsShortTextWhole(t *testing.T) {
	got := split("短い返事", bodyLimit)
	if len(got) != 1 || got[0] != "短い返事" {
		t.Fatalf("上限に収まる本文を分けている: %q", got)
	}
}

func TestSplitStaysUnderLimit(t *testing.T) {
	// 多バイト文字で埋める。バイト数で数えていると、まだ余裕があるのに
	// 分けるか、超えているのに分けないかのどちらかになる。
	line := strings.Repeat("あ", 120)
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString(line + "\n")
	}

	parts := split(b.String(), bodyLimit)
	if len(parts) < 2 {
		t.Fatalf("4800 文字が %d 個のまま", len(parts))
	}
	for i, p := range parts {
		if n := utf8.RuneCountInString(p); n > bodyLimit {
			t.Fatalf("%d 個目が %d 文字ある", i, n)
		}
	}
	// 中身は失われない。
	joined := strings.ReplaceAll(strings.Join(parts, ""), "\n", "")
	if !strings.Contains(joined, line) {
		t.Fatal("分けた結果から本文が欠けている")
	}
}

func TestSplitClosesCodeFence(t *testing.T) {
	var b strings.Builder
	b.WriteString("説明です\n")
	b.WriteString("```go\n")
	for i := 0; i < 300; i++ {
		b.WriteString("fmt.Println(\"ながい行をならべてうめる\")\n")
	}
	b.WriteString("```\n")

	parts := split(b.String(), bodyLimit)
	if len(parts) < 2 {
		t.Fatal("分かれていないので確かめられない")
	}
	for i, p := range parts {
		// 開いたまま切ると、続きのメッセージ全体がコードとして表示される。
		if n := strings.Count(p, "```"); n%2 != 0 {
			t.Fatalf("%d 個目のコードブロックが閉じていない:\n%s", i, tail(p, 80))
		}
	}
	if !strings.HasPrefix(parts[1], "```go") {
		t.Fatalf("続きがコードブロックを開き直していない: %q", parts[1][:20])
	}
}

func TestSummarizePicksTellingArgument(t *testing.T) {
	got := summarize(map[string]any{"timeout": 30, "command": "go test ./..."})
	if !strings.Contains(got, "go test") {
		t.Fatalf("何をしようとしているかが出ていない: %q", got)
	}
}

func TestClipCountsRunes(t *testing.T) {
	got := clip(strings.Repeat("あ", 50), 10)
	if n := utf8.RuneCountInString(got); n > 10 {
		t.Fatalf("%d 文字ある", n)
	}
	if strings.Contains(got, "\uFFFD") {
		t.Fatal("文字の途中で切れている")
	}
}

func TestTailKeepsEnd(t *testing.T) {
	got := tail("abcdefghij", 3)
	if !strings.HasSuffix(got, "hij") {
		t.Fatalf("末尾が残っていない: %q", got)
	}
}
