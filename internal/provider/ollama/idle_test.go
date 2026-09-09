package ollama

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// 何も届かないまま待ち続けない。
//
// 別の端末の Ollama を VPN 越しに使うと、通路は黙って落ちる。落ちたことは
// どちらの側にも伝わらないので、時間切れが無ければ永久に待つ。実際に
// 「道具の結果を返したあと、そのまま動かない」形で起きた。

// hang は応答の頭だけ返して、そのあと何も書かないサーバー。
func hang(t *testing.T, idle time.Duration) *Client {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-stop
	}))
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})
	c := New(srv.URL)
	c.SetIdle(idle)
	return c
}

func TestIdleTimeoutIsReported(t *testing.T) {
	c := hang(t, 150*time.Millisecond)

	stream, err := c.Chat(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		// 頭すら返らなければここで失敗する。どちらでも、理由が読めること。
		if !errors.Is(err, provider.ErrUnavailable) {
			t.Fatalf("提供元の失敗として返っていない: %v", err)
		}
		return
	}

	var got provider.Event
	select {
	case ev, ok := <-stream:
		if !ok {
			t.Fatal("何も言わずに閉じた")
		}
		got = ev
	case <-time.After(5 * time.Second):
		t.Fatal("時間切れが効かず、待ち続けている")
	}

	if got.Type != provider.EventError {
		t.Fatalf("種類 = %v, want error (黙って終わると、答え終わったように見える)", got.Type)
	}
	if !errors.Is(got.Err, provider.ErrUnavailable) {
		t.Errorf("提供元の失敗として返っていない: %v", got.Err)
	}
	// 次に何をすればよいかを添える。区別が付かないことは、そう書く。
	for _, want := range []string{"応答が届きませんでした", "接続"} {
		if !strings.Contains(got.Err.Error(), want) {
			t.Errorf("理由に %q が無い: %v", want, got.Err)
		}
	}
}

// 利用者が止めたときは失敗にしない。ここまでの出力は保存済みで、
// 止めたのは本人である。
func TestUserAbortIsNotAnIdleTimeout(t *testing.T) {
	c := hang(t, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := c.Chat(ctx, provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("繋がらない: %v", err)
	}
	cancel()

	select {
	case ev, ok := <-stream:
		if ok && ev.Type == provider.EventError {
			t.Errorf("中断が失敗として返っている: %v", ev.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("中断で返らない")
	}
}

// 届いているあいだは切らない。長い生成を時間で切ると、長い仕事ができなくなる。
func TestSlowButAliveStreamIsNotCut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// 見張りの上限より短い間隔で、少しずつ返す。合わせると上限を超える。
		for i := 0; i < 6; i++ {
			time.Sleep(60 * time.Millisecond)
			io.WriteString(w, `{"message":{"role":"assistant","content":"あ"},"done":false}`+"\n")
			w.(http.Flusher).Flush()
		}
		io.WriteString(w, `{"message":{"role":"assistant","content":""},"done":true}`+"\n")
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.SetIdle(150 * time.Millisecond)

	stream, err := c.Chat(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("繋がらない: %v", err)
	}
	var text string
	for ev := range stream {
		if ev.Type == provider.EventError {
			t.Fatalf("届いているのに切られた: %v", ev.Err)
		}
		if ev.Type == provider.EventDelta {
			text += ev.Text
		}
	}
	if text != "ああああああ" {
		t.Errorf("本文 = %q", text)
	}
}

// 使い回している接続が VPN の切断で死ぬことがある。死んだことはこちらに
// 伝わらないので、掴んでから分かる。1 度だけ繋ぎ直して送り直す。
func TestStaleConnectionIsRetriedOnce(t *testing.T) {
	var conns, requests int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if atomic.AddInt32(&requests, 1) == 1 {
			// 1 本目は応答を書かずに切る。死んだ接続を掴んだのと同じ形。
			if hj, ok := w.(http.Hijacker); ok {
				c, _, err := hj.Hijack()
				if err == nil {
					c.Close()
					return
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		io.WriteString(w, `{"message":{"role":"assistant","content":"はい"},"done":true}`+"\n")
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			atomic.AddInt32(&conns, 1)
		}
	}
	srv.Start()
	defer srv.Close()

	c := New(srv.URL)
	stream, err := c.Chat(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("やり直しても繋がらない: %v", err)
	}
	var text string
	for ev := range stream {
		if ev.Type == provider.EventError {
			t.Fatalf("やり直しが効いていない: %v", ev.Err)
		}
		if ev.Type == provider.EventDelta {
			text += ev.Text
		}
	}
	if text != "はい" {
		t.Errorf("本文 = %q", text)
	}
	if n := atomic.LoadInt32(&requests); n != 2 {
		t.Errorf("要求の数 = %d, want 2 (1 度だけやり直す)", n)
	}
	if n := atomic.LoadInt32(&conns); n < 2 {
		t.Errorf("繋ぎ直していない: 接続 = %d", n)
	}
}

// 利用者が止めたときはやり直さない。止めたのは本人である。
func TestAbortIsNotRetried(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	if _, err := New(srv.URL).Chat(ctx, provider.Request{Model: "m"}); err == nil {
		t.Fatal("中断したのに繋がった")
	}
	if n := atomic.LoadInt32(&requests); n != 1 {
		t.Errorf("要求の数 = %d, want 1 (中断はやり直さない)", n)
	}
}
