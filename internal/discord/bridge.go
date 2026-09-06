package discord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/engine"
	"github.com/LyraStellate/Ivis/internal/store"
)

// Deps は Bridge の構築に必要な部品。
//
// 実行そのものを関数で受けるのは、Discord 側の筋道 (メンションから会話を
// 引き当て、経過を描き、承認を受ける) を、モデルも提供元も無しで試せる
// ようにするためである。
type Deps struct {
	Cfg    *config.Config
	Store  *store.Store
	Agents *agent.Set
	// Runs は Web と共有する実行の占有。共有しないと、同じ会話が両方の
	// 入口から同時に走る。
	Runs *engine.Runs
	// Run は 1 ターンを実行する。既定では engine.Engine.Run。
	Run func(ctx context.Context, sessionID, text string, emit engine.Emit) error
	Now func() time.Time
	Log func(format string, args ...any)
}

// Bridge は Discord と Ivis を繋ぐ。
type Bridge struct {
	deps Deps

	mu sync.Mutex
	// active は Discord 側がいま動かしている会話。承認をどちらで受けるかの
	// 判定にも使う。
	active map[string]*run
	// asker は会話ごとの、いま依頼した人。承認を押せる相手を絞るのに使う。
	asker map[string]string
	waits map[string]*pending
	// answers は答えを待っている問い。会話ごとに高々 1 つ。
	answers map[string]*asking

	conn      *conn
	connected bool
	lastErr   string
	self      string
}

// New は Bridge を作る。接続はまだ張らない。
func New(d Deps) *Bridge {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = func(string, ...any) {}
	}
	return &Bridge{
		deps:    d,
		active:  map[string]*run{},
		asker:   map[string]string{},
		waits:   map[string]*pending{},
		answers: map[string]*asking{},
	}
}

// Status は接続の状態を返す。トークンを間違えたことに気付けるよう、
// 失敗の理由もそのまま返す。
func (b *Bridge) Status() (connected bool, err string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.connected, b.lastErr
}

// incoming は受け取ったメッセージのうち、Ivis が要る分だけ。
type incoming struct {
	ChannelID  string
	MessageID  string
	AuthorID   string
	AuthorName string
	Content    string
}

// serve は 1 件のメンションを処理する。
func (b *Bridge) serve(ctx context.Context, api API, in incoming) {
	text := strip(in.Content, b.selfID())
	if text == "" {
		b.reply(ctx, api, in, "御用でしたら、続けて内容を書いてください。")
		return
	}
	// コマンドかどうかを先に見る。エージェントへ流してから判断させると、
	// 止めたいときに、止めてほしいという依頼が生成の順番待ちに並ぶ。
	if name, arg, ok := parseCommand(text); ok {
		b.reply(ctx, api, in, b.runCommand(ctx, in, name, arg))
		return
	}

	sess, err := b.session(ctx, api, in)
	if err != nil {
		b.deps.Log("discord: 会話を用意できませんでした: %v", err)
		b.reply(ctx, api, in, err.Error())
		return
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !b.deps.Runs.Begin(sess.ID, cancel) {
		// 走っているターンが問いかけているなら、この呼びかけはその答えである。
		// 別の依頼として断ると、問われた人は答える手段を持たない。
		if b.answer(sess.ID, in.AuthorID, text) {
			return
		}
		// 待ち行列は作らない。断られたことが分かれば、言い直すか待てる。
		b.reply(ctx, api, in, "いまこのチャンネルの別の依頼を処理しています。終わってからもう一度呼んでください。")
		return
	}
	defer b.deps.Runs.End(sess.ID)

	r := newRun(api, in.ChannelID, in.MessageID, b.nameFor, b.deps.Now)
	b.enter(sess.ID, in.AuthorID, r)
	defer b.leave(sess.ID)

	stopTyping := b.typing(runCtx, api, in.ChannelID, r)
	defer stopTyping()

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		r.pump(runCtx, done, func(err error) {
			b.deps.Log("discord: 送信に失敗しました: %v", err)
		})
	}()

	// 誰の発言かを本文に含める。チャンネルには複数の人が居るので、これが
	// 無いと会話が成り立たない (#617204)。
	err = b.deps.Run(runCtx, sess.ID, in.AuthorName+": "+text, r.emit)
	// 送る側が止まるまで待つ。待たずに最後の反映へ進むと、同じ会話へ
	// 2 つの送信が重なる。
	close(done)
	<-stopped
	if err != nil && !engine.Reported(err) && runCtx.Err() == nil {
		r.emit(engine.Event{Type: engine.EvtError, Error: err.Error(), Kind: engine.KindOf(err)})
	}
	// 停止は失敗ではない。押した人はそうしたくて押している。
	r.finish(runCtx.Err() != nil)

	// 終わりは間隔を待たずに最終形を出す。
	if err := r.flush(context.WithoutCancel(runCtx), true); err != nil {
		b.deps.Log("discord: 最後の反映に失敗しました: %v", err)
	}
}

// session はチャンネルに対応する会話を返す。無ければ作る。
func (b *Bridge) session(ctx context.Context, api API, in incoming) (*store.Session, error) {
	sess, err := b.deps.Store.SessionByChannel(ctx, in.ChannelID)
	if err == nil {
		return sess, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	agentID := b.deps.Cfg.DefaultAgent
	if _, ok := b.deps.Agents.Get(agentID); !ok {
		return nil, fmt.Errorf("規定エージェント %s の定義が見つかりません", agentID)
	}

	// 表題はチャンネル名にする。チャンネルごとに 1 本である以上、名前は
	// 話題ではなく場所であるべきなので、最初の依頼で上書きさせない。
	title := "Discord"
	if n := api.ChannelName(ctx, in.ChannelID); n != "" {
		title = "#" + n
	}

	sess, err = b.deps.Store.CreateChannelSession(ctx, agentID, title, in.ChannelID)
	if err != nil {
		// 同時に 2 件届いた場合は片方が索引に弾かれる。引き直せば足りる。
		if s2, e2 := b.deps.Store.SessionByChannel(ctx, in.ChannelID); e2 == nil {
			return s2, nil
		}
		return nil, err
	}
	_ = os.MkdirAll(b.deps.Cfg.SessionWorkspace(sess.ID), 0o755)
	return sess, nil
}

// typing は「入力中」を出し続ける。承認待ちの間は止める。
func (b *Bridge) typing(ctx context.Context, api API, channelID string, r *run) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		// 出るのは約 10 秒なので、切れる前に送り直す。
		tick := time.NewTicker(8 * time.Second)
		defer tick.Stop()
		for {
			if !r.waiting() {
				_ = api.Typing(ctx, channelID)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return cancel
}

func (b *Bridge) reply(ctx context.Context, api API, in incoming, text string) {
	if _, err := api.Send(ctx, in.ChannelID, in.MessageID, Payload{Content: text}); err != nil {
		b.deps.Log("discord: 返信できませんでした: %v", err)
	}
}

// nameFor は表示名。定義が消えていても ID は残るので、そのまま出す。
func (b *Bridge) nameFor(agentID string) string {
	if a, ok := b.deps.Agents.Get(agentID); ok && a.Name != "" {
		return a.Name
	}
	return agentID
}

func (b *Bridge) enter(sessionID, asker string, r *run) {
	b.mu.Lock()
	b.active[sessionID] = r
	b.asker[sessionID] = asker
	b.mu.Unlock()
}

func (b *Bridge) leave(sessionID string) {
	b.mu.Lock()
	delete(b.active, sessionID)
	delete(b.asker, sessionID)
	b.mu.Unlock()
}

func (b *Bridge) selfID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.self
}

// strip はメンションの表記を取り除く。表記は <@id> と <@!id> の 2 通りあり、
// どちらで来るかは相手の書き方による。
func strip(content, selfID string) string {
	if selfID != "" {
		content = strings.ReplaceAll(content, "<@"+selfID+">", " ")
		content = strings.ReplaceAll(content, "<@!"+selfID+">", " ")
	}
	return strings.TrimSpace(strings.Join(strings.Fields(content), " "))
}
