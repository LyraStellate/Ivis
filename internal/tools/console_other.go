//go:build !windows

package tools

// decodeConsole はコマンドの出力を文字列にする。Windows 以外では、出力は
// UTF-8 であることが前提にできるため何もしない。
func decodeConsole(b []byte) string { return string(b) }
