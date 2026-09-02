// Package ollama は Ollama を provider.Provider として実装する。
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LyraStellate/Ivis/internal/provider"
)

// Client は Ollama への接続。
type Client struct {
	baseURL string
	http    *http.Client
}

// New は指定した接続先の Client を返す。
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// 生成は長く続きうるので全体のタイムアウトは置かない。中断は ctx で行う。
		http: &http.Client{},
	}
}

// Name は表示用の名前。
func (c *Client) Name() string { return "ollama" }

type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
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
}

type chatChunk struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// Chat は生成を開始する。返されたチャネルは必ず done か error で終わる。
func (c *Client) Chat(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body := chatRequest{Model: req.Model, Stream: true, Options: req.Options}
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
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %v", provider.ErrUnavailable, c.baseURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, classify(req.Model, resp.StatusCode, raw)
	}

	out := make(chan provider.Event, 32)
	go c.stream(ctx, resp, req.Model, out)
	return out, nil
}

func (c *Client) stream(ctx context.Context, resp *http.Response, model string, out chan<- provider.Event) {
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

	for {
		var chunk chatChunk
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() != nil {
				// 利用者による中断。ここまでの出力は既に流してある。
				send(provider.Event{Type: provider.EventDone})
				return
			}
			send(provider.Event{Type: provider.EventError, Err: fmt.Errorf("応答の解釈に失敗しました: %w", err)})
			return
		}
		if chunk.Error != "" {
			send(provider.Event{Type: provider.EventError, Err: classify(model, 0, []byte(chunk.Error))})
			return
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
			break
		}
	}

	if len(calls) > 0 {
		if !send(provider.Event{Type: provider.EventToolCalls, ToolCalls: calls}) {
			return
		}
	}
	send(provider.Event{Type: provider.EventDone})
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
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
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
