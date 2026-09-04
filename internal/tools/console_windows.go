//go:build windows

package tools

import (
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleOutCP = kernel32.NewProc("GetConsoleOutputCP")
	procGetACP          = kernel32.NewProc("GetACP")
	procMultiByteToWide = kernel32.NewProc("MultiByteToWideChar")
	procGetOEMCP        = kernel32.NewProc("GetOEMCP")
)

const cpUTF8 = 65001

// decodeConsole はコマンドの出力を文字列にする。
//
// Windows のコマンドは UTF-8 ではなく、その環境のコードページ (日本語なら
// cp932) で書き出すことがある。そのまま文字列にすると読めない列になる。
// 変換の表を自前で持たず、OS に持たせる。
//
// 妥当な UTF-8 のときは触らない。近年の道具は UTF-8 で書き出すため、
// 一律に変換すると今度はそちらが壊れる。
func decodeConsole(b []byte) string {
	if len(b) == 0 || utf8.Valid(b) {
		return string(b)
	}
	for _, cp := range []uint32{consoleCP(), oemCP(), ansiCP()} {
		if cp == 0 || cp == cpUTF8 {
			continue
		}
		if s, ok := fromCodePage(cp, b); ok {
			return s
		}
	}
	return string(b)
}

func consoleCP() uint32 {
	// 出力を横取りしているときは端末が無く、0 が返ることがある。
	r, _, _ := procGetConsoleOutCP.Call()
	return uint32(r)
}

func oemCP() uint32 {
	r, _, _ := procGetOEMCP.Call()
	return uint32(r)
}

func ansiCP() uint32 {
	r, _, _ := procGetACP.Call()
	return uint32(r)
}

// fromCodePage は指定のコードページとして読み直す。読めなければ偽を返す。
func fromCodePage(cp uint32, b []byte) (string, bool) {
	// 1 回目は必要な長さを尋ねる。MB_ERR_INVALID_CHARS (8) を渡し、その
	// コードページとして筋の通らない列なら失敗させる。
	const errInvalidChars = 8
	n, _, _ := procMultiByteToWide.Call(
		uintptr(cp), errInvalidChars,
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return "", false
	}

	buf := make([]uint16, n)
	got, _, _ := procMultiByteToWide.Call(
		uintptr(cp), errInvalidChars,
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		uintptr(unsafe.Pointer(&buf[0])), n)
	if got == 0 {
		return "", false
	}
	return string(utf16.Decode(buf[:got])), true
}
