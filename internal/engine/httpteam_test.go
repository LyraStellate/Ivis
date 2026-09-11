package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/provider/ollama"
)

// チームの手番の切り替わりを、本物の HTTP 越しに回す。
//
// ほかの試験は provider を差し替えて通路そのものを飛ばしているので、接続の
// 使い回しや応答の読み残しといった、通路の側の壊れ方が出てこない。実際に
// 「手番が切り替わるところで提供元に繋がらなくなる」形で起きた。

// fakeOllama は Ollama の受け口を最小限だけ真似る。
type fakeOllama struct {
	mu sync.Mutex
	// reply は n 回目の /api/chat が返す NDJSON の行。
	reply func(n int, body map[string]any) []string
	chats int
	shows int
	// conns は張られた接続の数。使い回せていれば 1 本で済む。
	conns int
}

func (f *fakeOllama) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/show"):
			f.mu.Lock()
			f.shows++
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model_info": map[string]any{"fake.context_length": 8192},
			})
		case strings.HasPrefix(r.URL.Path, "/api/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
		case strings.HasPrefix(r.URL.Path, "/api/chat"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			n := f.chats
			f.chats++
			f.mu.Unlock()

			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			for _, line := range f.reply(n, body) {
				fmt.Fprintln(w, line)
				w.(http.Flusher).Flush()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			f.mu.Lock()
			f.conns++
			f.mu.Unlock()
		}
	}
	t.Cleanup(srv.Close)
	return srv
}

// chunk は 1 行ぶんの NDJSON を組む。
func chunk(o map[string]any) string {
	b, _ := json.Marshal(o)
	return string(b)
}

func line(text string) string {
	return chunk(map[string]any{"message": map[string]any{"role": "assistant", "content": text}})
}

func callSend(to string) string {
	return chunk(map[string]any{"message": map[string]any{
		"role": "assistant", "content": "",
		"tool_calls": []any{map[string]any{"function": map[string]any{
			"name": "send_message",
			"arguments": map[string]any{
				"to": to, "why": "頼まれた", "did": "やった", "message": "どうぞ",
			},
		}}},
	}})
}

func finish() string {
	return chunk(map[string]any{
		"message": map[string]any{"role": "assistant", "content": ""},
		"done":    true, "done_reason": "stop",
		"prompt_eval_count": 100, "eval_count": 10,
	})
}

// 手番の切り替わりで、使い回した接続が死んでいても止まらないこと。
//
// 利用者の報告そのものである — 報告や指示で担当が切り替わるところで、提供元に
// 繋がらないと出て進まなくなる。直列の会話では 1 ターンに数回しか生成しない
// ので目に見えにくいが、チームは 1 ラウンドで何十回も生成し、手番の切り替えごとに
// 間が空くぶん、死んだ接続を掴む機会が多い。
func TestTurnSwitchSurvivesDeadConnection(t *testing.T) {
	// 1 本目の接続は 1 回だけ答え、その後その接続に来た要求には何も返さない。
	// 経路が張り直されて通じなくなった状態である。
	var mu sync.Mutex
	served := map[int]int{}
	nextID := 0
	stop := make(chan struct{})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/show") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model_info": map[string]any{"fake.context_length": 8192},
			})
			return
		}
		id, _ := r.Context().Value(engConnKey{}).(int)
		mu.Lock()
		served[id]++
		n := served[id]
		first := len(served) == 1
		mu.Unlock()

		if first && n > 1 {
			<-stop
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		var lines []string
		mu.Lock()
		total := 0
		for _, v := range served {
			total += v
		}
		mu.Unlock()
		if total == 1 {
			lines = []string{line("配ります"), callSend("hand"), finish()}
		} else {
			lines = []string{line("できました"), finish()}
		}
		for _, l := range lines {
			fmt.Fprintln(w, l)
			w.(http.Flusher).Flush()
		}
	})

	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
		mu.Lock()
		nextID++
		id := nextID
		mu.Unlock()
		return context.WithValue(ctx, engConnKey{}, id)
	}
	srv.Start()
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})

	f, sess := teamFixture(t, func(int, provider.Request) []provider.Event { return nil })
	cli := ollama.New(srv.URL)
	// 無応答の上限は長いまま。ここで見たいのは、その上限を待たずに張り直して
	// 手番を続けられるかである。
	cli.SetIdle(30 * time.Second)
	cli.SetHead(300 * time.Millisecond)
	f.eng.Provider = cli

	start := time.Now()
	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("張り直しを待って %v 止まっている", d)
	}
	for _, e := range f.events {
		if e.Type == EvtError {
			t.Fatalf("手番の切り替わりで失敗した: kind=%s %s", e.Kind, e.Error)
		}
	}
	// 切り替わった先の手番が実際に走っていること。
	if got := f.turns(); len(got) < 2 {
		t.Fatalf("手番 = %v (切り替わっていない)", got)
	}
}

// engConnKey は接続ごとの通し番号を要求から引くための鍵。
type engConnKey struct{}

// 応答が大きいと、読み終える前に打ち切ることになる。そのとき接続が
// 使い回せているか。
//
// 本文が短ければ、JSON の読み取りが先読みで最後まで飲み込んでしまうので、
// この壊れ方は出てこない。実際の生成は数千字あり、そちらが常態である。
func TestConnectionIsReusedAcrossTurns(t *testing.T) {
	long := strings.Repeat("あ", 4000)
	fake := &fakeOllama{}
	fake.reply = func(n int, body map[string]any) []string {
		switch n {
		case 0:
			return []string{line(long), callSend("hand"), finish()}
		case 1:
			return []string{line(long), callSend("lead"), finish()}
		default:
			return []string{line(long), finish()}
		}
	}
	srv := fake.start(t)

	f, sess := teamFixture(t, func(int, provider.Request) []provider.Event { return nil })
	f.eng.Provider = ollama.New(srv.URL)

	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fake.mu.Lock()
	conns, chats := fake.conns, fake.chats
	fake.mu.Unlock()
	if chats < 3 {
		t.Fatalf("生成が %d 回", chats)
	}
	// 1 本で足りる。生成のたびに張り直していれば、生成の数だけ増える。
	if conns > 2 {
		t.Errorf("接続を %d 本張っている (生成 %d 回)。使い回せていない", conns, chats)
	}
}

// 手番が 3 つ続いても、どの手番も提供元に繋がること。
func TestTeamTurnsOverRealHTTP(t *testing.T) {
	fake := &fakeOllama{}
	fake.reply = func(n int, body map[string]any) []string {
		// 1 手目: lead が hand へ渡す。2 手目: hand が lead へ返す。
		// 3 手目: lead が黙って終える。
		switch n {
		case 0:
			return []string{line("配ります"), callSend("hand"), finish()}
		case 1:
			return []string{line("できました"), callSend("lead"), finish()}
		default:
			return []string{line("受け取りました"), finish()}
		}
	}
	srv := fake.start(t)

	f, sess := teamFixture(t, func(int, provider.Request) []provider.Event { return nil })
	f.eng.Provider = ollama.New(srv.URL)

	if err := f.eng.Run(context.Background(), sess.ID, "@lead やって", f.emit); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, e := range f.events {
		if e.Type == EvtError {
			t.Fatalf("手番の途中で失敗した: kind=%s %s", e.Kind, e.Error)
		}
	}
	if fake.chats < 3 {
		t.Fatalf("生成が %d 回しか走っていない", fake.chats)
	}
	if got := f.turns(); len(got) < 3 {
		t.Fatalf("手番 = %v", got)
	}

	// 接続は使い回せていること。生成のたびに張り直すと、手番の数だけ
	// 繋ぎ直すことになる。近い相手なら見えないが、VPN 越しではその 1 回
	// ごとに失敗する余地ができる。
	if fake.conns > 2 {
		t.Errorf("接続を %d 本張っている (生成 %d 回)。使い回せていない",
			fake.conns, fake.chats)
	}
}
