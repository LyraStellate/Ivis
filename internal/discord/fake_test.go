package discord

import (
	"context"
	"fmt"
	"sync"
)

// fakeMsg は Discord 上の 1 通の代わり。
type fakeMsg struct {
	id      string
	reply   string
	p       Payload
	edits   int
	deleted bool
}

// fakeAPI は送った内容を記録するだけの相手。「入力中」は別の goroutine から
// 来るので、記録は錠で守る。
type fakeAPI struct {
	mu   sync.Mutex
	msgs []*fakeMsg
	// fail は次の 1 回だけ返す失敗。
	fail  error
	typed int
	name  string
}

func (f *fakeAPI) Send(_ context.Context, _, replyTo string, p Payload) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		err := f.fail
		f.fail = nil
		return "", err
	}
	m := &fakeMsg{id: fmt.Sprintf("m%d", len(f.msgs)+1), reply: replyTo, p: p}
	f.msgs = append(f.msgs, m)
	return m.id, nil
}

func (f *fakeAPI) Edit(_ context.Context, _, messageID string, p Payload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		err := f.fail
		f.fail = nil
		return err
	}
	for _, m := range f.msgs {
		if m.id == messageID {
			m.p = p
			m.edits++
			return nil
		}
	}
	return fmt.Errorf("そんなメッセージはない: %s", messageID)
}

func (f *fakeAPI) Delete(_ context.Context, _, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		err := f.fail
		f.fail = nil
		return err
	}
	for _, m := range f.msgs {
		if m.id == messageID {
			m.deleted = true
			return nil
		}
	}
	return nil
}

func (f *fakeAPI) Typing(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed++
	return nil
}

func (f *fakeAPI) ChannelName(context.Context, string) string { return f.name }

// live は消されていないメッセージを投稿順に返す。
func (f *fakeAPI) live() []fakeMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeMsg
	for _, m := range f.msgs {
		if !m.deleted {
			out = append(out, *m)
		}
	}
	return out
}

// all は消したものも含めて投稿順に返す。
func (f *fakeAPI) all() []fakeMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeMsg, 0, len(f.msgs))
	for _, m := range f.msgs {
		out = append(out, *m)
	}
	return out
}

func (f *fakeAPI) typedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typed
}
