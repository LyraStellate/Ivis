// Package ollama は Ollama を provider.Provider として実装する。
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// Client は Ollama への接続。
type Client struct {
	baseURL string
	http    *http.Client
	// idle は何も届かないまま待つ上限。
	idle time.Duration
	// probe は生きているかを尋ねる要求の待ち時間。生成とは別に持つ。
	probe time.Duration
}

// New は指定した接続先の Client を返す。
// DefaultIdle は、提供元から何も届かないまま待つ上限。
//
// 全体の時間には上限を置かない。生成は長く続きうるもので、時間で切ると
// 長い仕事ができなくなる。上限を置くのは「何も届かない時間」のほうで、
// これはモデルが考えている間ではなく、通路が死んでいる間に伸びる。
//
// 別の端末の Ollama を VPN 越しに使うと、通路は黙って落ちる。落ちたことは
// どちらの側にも伝わらないので、時間切れが無ければ永久に待つ。実際に
// 「道具の結果を返したあと、そのまま動かない」形で起きた。
//
// 5 分にしてあるのは、大きなモデルの読み込みがそこまで伸びうるためである。
const DefaultIdle = 5 * time.Minute

// DefaultProbe は、生きているかを尋ねる要求の待ち時間。
//
// 生成と違って、これは待っても意味が変わらない要求である。ただし短すぎると、
// 別の端末の Ollama を VPN 越しに使っているときに、動いている相手を落ちて
// いると判じてしまう。3 秒では足りなかった。
const DefaultProbe = 10 * time.Second

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// 生成は長く続きうるので全体のタイムアウトは置かない。中断は ctx で行う。
		http:  &http.Client{Transport: transport()},
		idle:  DefaultIdle,
		probe: DefaultProbe,
	}
}

// transport は接続の使い回し方を決める。
//
// 既定の設定のままだと、使い回す接続を長く抱えたままにする。VPN が切れると
// その接続は黙って死に、こちらはそれを知らないまま次の要求で掴んで失敗する。
// 「モデル提供元に接続できません」が続けて出るのはこれである。
//
// 抱える時間を短くして、死んだ接続を掴む窓を狭める。使い回しをやめないのは、
// 1 ターンの中で何度も往復するためで、毎回繋ぎ直すほうが遅い。
func transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.IdleConnTimeout = 30 * time.Second
	t.MaxIdleConnsPerHost = 4
	// 相手が生きているかを、繋いだあとも定期的に確かめる。
	t.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 15 * time.Second,
	}).DialContext
	return t
}

// SetProbe は生きているかを尋ねる要求の待ち時間を変える。
func (c *Client) SetProbe(d time.Duration) {
	if d > 0 {
		c.probe = d
	}
}

// SetIdle は何も届かないまま待つ上限を変える。0 以下なら見張らない。
func (c *Client) SetIdle(d time.Duration) { c.idle = d }

// Name は表示用の名前。
func (c *Client) Name() string { return "ollama" }

type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
}

type chatToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type chatToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Tools    []chatToolDef  `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
	// Think は必ず明示して送る。省くとモデルの既定に従うため、切ってあっても
	// 既定で考えるモデルは考え続ける。false を断る実装のために項目を落とせる
	// よう、省略と false は区別できる形にしてある。
	Think *bool `json:"think,omitempty"`
}

type chatChunk struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
	// DoneReason は終わり方。"length" はコンテキストが尽きて打ち切られたことを指す。
	DoneReason string `json:"done_reason"`
	// 最後のチャンクにだけ載る。入力に何トークン使ったかの実測値。
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

// Chat は生成を開始する。返されたチャネルは必ず done か error で終わる。
func (c *Client) Chat(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body := chatRequest{Model: req.Model, Stream: true, Options: req.Options}
	// 使うかどうかを毎回はっきり伝える。項目を出さないと Ollama はモデルの
	// 既定を採るので、推論を切った設定が既定で考えるモデルに効かない。
	think := req.Think
	body.Think = &think
	for _, m := range req.Messages {
		cm := chatMessage{Role: m.Role, Content: m.Content, ToolName: m.ToolName}
		for _, tc := range m.ToolCalls {
			var ctc chatToolCall
			ctc.Function.Name = tc.Name
			ctc.Function.Arguments = tc.Arguments
			cm.ToolCalls = append(cm.ToolCalls, ctc)
		}
		body.Messages = append(body.Messages, cm)
	}
	for _, t := range req.Tools {
		var td chatToolDef
		td.Type = "function"
		td.Function.Name = t.Name
		td.Function.Description = t.Description
		td.Function.Parameters = t.Parameters
		body.Tools = append(body.Tools, td)
	}

	// 何も届かない時間を見張る。届くたびに数え直すので、長い生成は
	// 妨げない。伸びるのは通路が死んでいるときだけである。
	runCtx, cancel := context.WithCancel(ctx)
	w := newWatch(c.idle, cancel)

	resp, err := c.post(runCtx, body)
	if err != nil {
		// 推論を持たないモデルへ think を送ると断る実装がある。切ってある
		// ときに限り、項目を落として送り直す。使わないと言っただけで会話が
		// 始まらないのは筋が通らない。
		var unsupported *provider.ThinkingUnsupportedError
		if !req.Think && errors.As(err, &unsupported) {
			body.Think = nil
			w.seen()
			resp, err = c.post(runCtx, body)
		}
		if err != nil {
			w.stop()
			cancel()
			if w.expired() {
				return nil, c.silent()
			}
			return nil, err
		}
	}
	// 応答の頭が来た。ここから先は本文が届くたびに数え直す。
	w.seen()

	out := make(chan provider.Event, 32)
	go func() {
		defer cancel()
		defer w.stop()
		c.stream(runCtx, resp, req.Model, out, w)
	}()
	return out, nil
}

// silent は、待っても何も届かなかったことを表す失敗。
//
// 提供元へ届いていないのか、届いたまま返らないのかは、こちらからは区別
// できない。区別できないことを、次に取れる手とともに書く。
func (c *Client) silent() error {
	return fmt.Errorf("%w (%s): %s のあいだ応答が届きませんでした。"+
		"接続 (VPN の切断など) か、モデルの読み込みが長すぎることが考えられます。"+
		"設定の「無応答の上限」を延ばすか、接続先を確かめてください",
		provider.ErrUnavailable, c.baseURL, c.idle)
}

// post は 1 回の生成要求を送り、応答の本体を返す。送り直すことがあるので
// 切り出してある。返した応答の本体を閉じるのは呼び出し側。
func (c *Client) post(ctx context.Context, body chatRequest) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil && ctx.Err() == nil {
		// 使い回している接続が VPN の切断で死んでいることがある。死んだことは
		// こちらに伝わらないので、掴んでから分かる。要求は本文を読み直せる
		// (GetBody を持つ) ので、1 度だけ繋ぎ直して送り直す。
		//
		// やり直すのは繋ぐ段階の失敗だけである。相手が受け取ったあとの失敗を
		// やり直すと、同じ生成が 2 度走る。
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat",
			bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		c.http.CloseIdleConnections()
		resp, err = c.http.Do(httpReq)
	}
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %v", provider.ErrUnavailable, c.baseURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, classify(body.Model, resp.StatusCode, raw)
	}
	return resp, nil
}

// watch は、何も届かないまま待ち続けないための見張り。
//
// 届くたびに seen で数え直す。数え直されないまま上限を過ぎたら、要求ごと
// 取り消す。取り消しは利用者による中断と同じ形で伝わるので、どちらだったかを
// expired で見分けられるようにしておく — 黙って終わったように見せると、
// 待っていた側は「終わったのか、落ちたのか」が分からない。
type watch struct {
	idle    time.Duration
	timer   *time.Timer
	timedUp atomic.Bool
}

func newWatch(idle time.Duration, cancel context.CancelFunc) *watch {
	w := &watch{idle: idle}
	if idle <= 0 {
		return w
	}
	w.timer = time.AfterFunc(idle, func() {
		w.timedUp.Store(true)
		cancel()
	})
	return w
}

func (w *watch) seen() {
	if w.timer != nil {
		w.timer.Reset(w.idle)
	}
}

func (w *watch) stop() {
	if w.timer != nil {
		w.timer.Stop()
	}
}

func (w *watch) expired() bool { return w.timedUp.Load() }

func (c *Client) stream(ctx context.Context, resp *http.Response, model string, out chan<- provider.Event, w *watch) {
	defer close(out)
	defer resp.Body.Close()

	send := func(e provider.Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	dec := json.NewDecoder(resp.Body)
	var calls []provider.ToolCall
	var usage *provider.Usage
	truncated := false

	for {
		var chunk chatChunk
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			if w.expired() {
				// 待っても何も届かなかった。中断と同じ形で切れるので、
				// ここで見分けて失敗として伝える。黙って終わったことに
				// すると、画面には答え終わったように見える。
				//
				// send は ctx を見るが、その ctx は見張りが閉じたものである。
				// 見る側で送ると、この最後の 1 件が届くかどうかが運になる。
				// ここだけは通路の空きだけを見て置く。
				select {
				case out <- provider.Event{Type: provider.EventError, Err: c.silent()}:
				default:
				}
				return
			}
			if ctx.Err() != nil {
				// 利用者による中断。ここまでの出力は既に流してある。
				send(provider.Event{Type: provider.EventDone})
				return
			}
			send(provider.Event{Type: provider.EventError, Err: fmt.Errorf("応答の解釈に失敗しました: %w", err)})
			return
		}
		// 何かが届いた。見張りを数え直す。
		w.seen()
		if chunk.Error != "" {
			send(provider.Event{Type: provider.EventError, Err: classify(model, 0, []byte(chunk.Error))})
			return
		}
		if chunk.Message.Thinking != "" {
			if !send(provider.Event{Type: provider.EventThinking, Text: chunk.Message.Thinking}) {
				return
			}
		}
		if chunk.Message.Content != "" {
			if !send(provider.Event{Type: provider.EventDelta, Text: chunk.Message.Content}) {
				return
			}
		}
		for _, tc := range chunk.Message.ToolCalls {
			calls = append(calls, provider.ToolCall{
				ID:        fmt.Sprintf("call_%d", len(calls)+1),
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		if chunk.Done {
			// 上限に当たって止まったのか、言い終えたのかは、ここでしか
			// 分からない。伝えないと、途中で切れた応答が完成品として残る。
			truncated = chunk.DoneReason == "length"
			if chunk.PromptEvalCount > 0 {
				usage = &provider.Usage{
					PromptTokens: chunk.PromptEvalCount,
					EvalTokens:   chunk.EvalCount,
				}
			}
			break
		}
	}

	if len(calls) > 0 {
		if !send(provider.Event{Type: provider.EventToolCalls, ToolCalls: calls}) {
			return
		}
	}
	send(provider.Event{Type: provider.EventDone, Usage: usage, Truncated: truncated})
}

type tagsResponse struct {
	Models []struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		Details struct {
			Family        string `json:"family"`
			ParameterSize string `json:"parameter_size"`
		} `json:"details"`
	} `json:"models"`
}

// Models は Ollama が保持しているモデルの一覧を返す。
func (c *Client) Models(ctx context.Context) ([]provider.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %v", provider.ErrUnavailable, c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, classify("", resp.StatusCode, raw)
	}
	var tr tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	out := make([]provider.Model, 0, len(tr.Models))
	for _, m := range tr.Models {
		out = append(out, provider.Model{
			Name:       m.Name,
			Size:       m.Size,
			Family:     m.Details.Family,
			Parameters: m.Details.ParameterSize,
		})
	}
	return out, nil
}

// Health は Ollama に到達できるかを確かめる。
func (c *Client) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.probe)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w (%s): %v", provider.ErrUnavailable, c.baseURL, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w (%s): status %d", provider.ErrUnavailable, c.baseURL, resp.StatusCode)
	}
	return nil
}

// classify は Ollama のエラー応答を、利用者が次に何をすべきか分かる型へ落とす。
// まとめて「エラーが発生しました」にすると、Ollama の起動忘れなのか設定の
// 誤りなのかを利用者が判断できなくなる。
func classify(model string, status int, raw []byte) error {
	msg := strings.TrimSpace(string(raw))
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err == nil && body.Error != "" {
		msg = body.Error
	}
	low := strings.ToLower(msg)

	switch {
	case strings.Contains(low, "does not support thinking"), strings.Contains(low, "thinking is not supported"):
		return &provider.ThinkingUnsupportedError{Model: model}
	case strings.Contains(low, "does not support tools"), strings.Contains(low, "tools are not supported"):
		return &provider.ToolsUnsupportedError{Model: model}
	case strings.Contains(low, "not found"), strings.Contains(low, "no such model"), status == http.StatusNotFound:
		return &provider.ModelNotFoundError{Model: model}
	}
	if status != 0 {
		return fmt.Errorf("Ollama がエラーを返しました (status %d): %s", status, msg)
	}
	return fmt.Errorf("Ollama がエラーを返しました: %s", msg)
}

// showResponse は /api/show の必要な部分だけを受ける。
type showResponse struct {
	// ModelInfo はアーキテクチャ名を接頭辞に持つ雑多な値の集まりで、コンテキスト長は
	// "<arch>.context_length" という名前で入る。名前が固定でないため、
	// 接尾辞で探す。
	ModelInfo map[string]any `json:"model_info"`
}

// ContextLength はモデルが持つコンテキスト長を返す。分からなければ 0 を返す。
// 分母が無いときに割合を出すと嘘になるので、呼び出し側はそれを見て諦める。
// probeCtx は、待っても意味の変わらない要求に待ち時間を掛ける。
func (c *Client) probeCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.probe)
}

func (c *Client) ContextLength(ctx context.Context, model string) (int, error) {
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w (%s): %v", provider.ErrUnavailable, c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return 0, classify(model, resp.StatusCode, raw)
	}

	var sr showResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return 0, err
	}
	for k, v := range sr.ModelInfo {
		if !strings.HasSuffix(k, ".context_length") {
			continue
		}
		// JSON の数値は float64 で入る。
		if f, ok := v.(float64); ok && f > 0 {
			return int(f), nil
		}
	}
	return 0, nil
}
