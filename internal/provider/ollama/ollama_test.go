package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// sent は 1 回の要求で送られた本体のうち、確かめたい部分だけを受ける。
type sent struct {
	Think *bool `json:"think"`
}

// record は要求を順に控え、あらかじめ決めた応答を返す試験用の Ollama。
// reply は要求の回数を受け取り、書き込む内容を決める。
func record(t *testing.T, reply func(n int, w http.ResponseWriter)) (*Client, *[]sent) {
	t.Helper()
	var got []sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		var s sent
		if err := json.Unmarshal(buf, &s); err != nil {
			t.Errorf("要求を読めません: %v", err)
		}
		got = append(got, s)
		reply(len(got), w)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &got
}

// done は 1 件だけ返して終わる正常な応答。
func done(w http.ResponseWriter) {
	io.WriteString(w, `{"message":{"role":"assistant","content":"はい"},"done":true}`+"\n")
}

func drain(t *testing.T, ch <-chan provider.Event) {
	t.Helper()
	for range ch {
	}
}

// 推論を切った設定は提供元へ伝わらなければ意味がない。項目を省くと Ollama は
// モデルの既定を採るため、既定で考えるモデルは考え続ける。
func TestThinkIsAlwaysStated(t *testing.T) {
	for _, tc := range []struct {
		name  string
		think bool
	}{{"切っている", false}, {"入れている", true}} {
		t.Run(tc.name, func(t *testing.T) {
			c, got := record(t, func(n int, w http.ResponseWriter) { done(w) })
			ch, err := c.Chat(context.Background(), provider.Request{Model: "m", Think: tc.think})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			drain(t, ch)

			if len(*got) != 1 {
				t.Fatalf("要求は 1 回のはずが %d 回", len(*got))
			}
			th := (*got)[0].Think
			if th == nil {
				t.Fatal("think が送られていない。省くとモデルの既定に従ってしまう")
			}
			if *th != tc.think {
				t.Fatalf("think = %v, 求めるのは %v", *th, tc.think)
			}
		})
	}
}

// 推論を持たないモデルへ think を送ると断る実装がある。使わないと言っただけで
// 会話が始まらないのは筋が通らないので、切ってあるときは項目を落として送り直す。
func TestFalseThinkRetriesWithoutTheField(t *testing.T) {
	c, got := record(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"\"m\" does not support thinking"}`)
			return
		}
		done(w)
	})

	ch, err := c.Chat(context.Background(), provider.Request{Model: "m", Think: false})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	drain(t, ch)

	if len(*got) != 2 {
		t.Fatalf("送り直しは 1 度のはずが、要求は %d 回", len(*got))
	}
	if th := (*got)[1].Think; th != nil {
		t.Fatalf("送り直しでは think を落とすはずが %v", *th)
	}
}

// 推論を求めたうえで断られたのは利用者へ伝えるべきことで、隠して送り直す
// ものではない。
func TestTrueThinkReportsUnsupported(t *testing.T) {
	c, got := record(t, func(n int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"\"m\" does not support thinking"}`)
	})

	_, err := c.Chat(context.Background(), provider.Request{Model: "m", Think: true})
	var unsupported *provider.ThinkingUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("推論に対応していないことが伝わらない: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("送り直してはならないのに要求は %d 回", len(*got))
	}
}
