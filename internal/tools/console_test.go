package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// Windows のコマンドは、その環境のコードページで書き出すことがある。
// UTF-8 として読むと文字が壊れ、モデルは結果を読めない。
func TestRunCommandDecodesConsoleOutput(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("コードページの読み直しは Windows でのみ意味を持つ")
	}
	ec := newCtx(t)
	out, err := (&runCommandTool{}).Execute(context.Background(), ec, map[string]any{
		// date は日付を表示したあと入力を求めて終わる。表示の部分が読めれば足りる。
		"command": "date",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("出力:\n%s", out)

	// 置換文字が並んでいたら読み直せていない。
	if strings.Contains(out, "\ufffd") {
		t.Errorf("文字が壊れています:\n%s", out)
	}
	if !strings.Contains(out, "現在の日付") && !strings.Contains(out, "current date") {
		t.Errorf("日付の見出しが読めていません:\n%s", out)
	}
}

// 妥当な UTF-8 は触らない。近年の道具は UTF-8 で書き出すため、一律に
// 変換すると今度はそちらが壊れる。
func TestDecodeConsoleLeavesUTF8(t *testing.T) {
	for _, s := range []string{"", "plain ascii", "日本語のまま", "絵文字 🙂"} {
		if got := decodeConsole([]byte(s)); got != s {
			t.Errorf("decodeConsole(%q) = %q", s, got)
		}
	}
}
