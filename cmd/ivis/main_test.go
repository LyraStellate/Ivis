package main

import (
	"net"
	"strings"
	"testing"
)

// 解決できない宛先。設定画面の説明に載っている例をそのまま貼ると、この形の
// 値が保存される。
const unreachable = "100.x.y.z:0"

// 届かない宛先が設定に残っているだけで起動できないと、それを直すための画面
// ごと開けなくなる。番号を保ったままループバックへ退く。
func TestConfiguredAddressFallsBackToLoopback(t *testing.T) {
	ln, err := listenOn(unreachable, false)
	if err != nil {
		t.Fatalf("退かずに失敗した。設定を直す画面へ入れなくなる: %v", err)
	}
	defer ln.Close()

	host, _, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("待ち受けの宛先を読めません: %v", err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("退き先が %s。手元からしか触れない宛先であるべき", host)
	}
}

// 番号は利用者が決めたもので、届かないのは宛先だけである。退いても番号は
// 変えない。
func TestFallbackKeepsThePort(t *testing.T) {
	// 空いている番号を借り、閉じてから同じ番号で試す。
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("待ち受けを開けません: %v", err)
	}
	_, port, _ := net.SplitHostPort(probe.Addr().String())
	probe.Close()

	ln, err := listenOn(net.JoinHostPort("100.x.y.z", port), false)
	if err != nil {
		t.Fatalf("listenOn: %v", err)
	}
	defer ln.Close()

	if _, got, _ := net.SplitHostPort(ln.Addr().String()); got != port {
		t.Fatalf("番号が %s へ変わった。求めるのは %s", got, port)
	}
}

// 命令行で渡された宛先は、利用者が今まさに選んだもの。黙って別の場所へ
// 束ね替えると、そこで待っているつもりの相手が届かない。
func TestFlagAddressDoesNotFallBack(t *testing.T) {
	ln, err := listenOn(unreachable, true)
	if err == nil {
		ln.Close()
		t.Fatal("指定された宛先へ束ねられないのに起動してしまった")
	}
	if !strings.Contains(err.Error(), unreachable) {
		t.Fatalf("どの宛先で失敗したのか分からない: %v", err)
	}
}
