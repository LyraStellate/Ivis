package ollama

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// 使い回した接続が黙って死んでいることがある (#903215)。
//
// VPN の経路が張り直されると、開いたままの接続はどちらにも通じなくなる。
// 落ちたことは伝わらないので、送っても応答の頭すら返らない。TCP が諦める
// までは分単位かかる。
//
// チームの手番はこれに当たりやすい。1 ラウンドで何十回も生成するうえ、手番の
// 切り替えごとに間が空くので、間に経路が張り直される機会がそのぶん増える。
// 直列の会話は 1 ターンに数回しか生成しないため、同じ壊れ方をしても目に
// 見えにくい。

// connKey は接続ごとの通し番号を要求から引くための鍵。
type connKey struct{}

// blackhole は、2 本目以降の要求を同じ接続で受けたときに黙り込むサーバー。
//
// 1 本目の接続で 1 回目の要求だけ答え、その接続に来た次の要求には何も返さない。
// 新しい接続なら答える。使い回した接続が死んだ状態そのものである。
func blackhole(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()

	var mu sync.Mutex
	served := map[int]int{}
	next := 0
	dials := 0
	stop := make(chan struct{})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := r.Context().Value(connKey{}).(int)
		mu.Lock()
		served[id]++
		n := served[id]
		mu.Unlock()

		if n > 1 {
			// この接続はもう通じない。何も返さないまま置く。
			<-stop
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"はい"}}` + "\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}` + "\n"))
		w.(http.Flusher).Flush()
	})

	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
		mu.Lock()
		next++
		id := next
		dials++
		mu.Unlock()
		return context.WithValue(ctx, connKey{}, id)
	}
	srv.Start()
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return dials
	}
}

// 死んだ接続を掴んだら、張り直して続ける。止まってはいけない。
func TestStaleConnectionIsRetried(t *testing.T) {
	srv, dials := blackhole(t)
	c := New(srv.URL)
	// 何も届かないまま待つ上限は長いまま。ここで見たいのは、その上限を
	// 待たずに張り直せるかである。
	c.SetIdle(30 * time.Second)
	c.SetHead(300 * time.Millisecond)

	ctx := context.Background()
	// 1 回目。ここで接続が 1 本張られ、応答のあとは使い回しの側へ回る。
	run1(t, c, ctx)

	// 2 回目。使い回した接続は死んでいる。張り直して答えが返ること。
	start := time.Now()
	run1(t, c, ctx)
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("張り直すまでに %v かかっている", d)
	}
	if n := dials(); n < 2 {
		t.Errorf("接続を張り直していない (%d 本)", n)
	}
}

// drain は 1 回の生成を最後まで読み、失敗があれば試験を落とす。
func run1(t *testing.T, c *Client, ctx context.Context) {
	t.Helper()
	stream, err := c.Chat(ctx, provider.Request{Model: "m"})
	if err != nil {
		t.Fatalf("生成を始められない: %v", err)
	}
	for ev := range stream {
		if ev.Type == provider.EventError {
			t.Fatalf("生成が失敗した: %v", ev.Err)
		}
	}
}

// 新しい接続には応答の頭までの期限を置かない。モデルの読み込みは分単位で
// 伸びうる。ここで切ると、大きなモデルが一度も使えなくなる。
func TestFreshConnectionIsNotCutShort(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		// 読み込みに時間がかかっている体で、頭を遅らせる。
		time.Sleep(400 * time.Millisecond)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"はい"},"done":true}` + "\n"))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	c.SetIdle(10 * time.Second)
	c.SetHead(100 * time.Millisecond)

	run1(t, c, context.Background())
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Errorf("新しい接続で %d 回送っている。頭が遅いだけで送り直してはいけない", n)
	}
}

// 期限を過ぎても張り直せないなら、理由が読める失敗として返る。
func TestStaleRetryStillFailsLoudly(t *testing.T) {
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-stop
	}))
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})

	c := New(srv.URL)
	c.SetIdle(400 * time.Millisecond)
	c.SetHead(100 * time.Millisecond)

	stream, err := c.Chat(context.Background(), provider.Request{Model: "m"})
	if err != nil {
		if !errors.Is(err, provider.ErrUnavailable) {
			t.Fatalf("提供元の失敗として返っていない: %v", err)
		}
		return
	}
	select {
	case ev, ok := <-stream:
		if !ok {
			t.Fatal("何も言わずに閉じた")
		}
		if ev.Type != provider.EventError {
			t.Fatalf("種類 = %v, want error", ev.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("待ち続けている")
	}
}
