package discord

import (
	"context"
	"errors"

	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/bwmarrin/discordgo"
)

// conn は Gateway への接続 1 本。
type conn struct {
	ses   *discordgo.Session
	api   API
	token string
}

// Apply は設定を接続へ反映する。有効でなければ切り、トークンが変わって
// いれば繋ぎ直す。再起動を要求しないのは、トークンを 1 つ直すために
// プロセスを落とさせるのが釣り合わないためである。
func (b *Bridge) Apply(d config.Discord) {
	b.mu.Lock()
	cur := ""
	if b.conn != nil {
		cur = b.conn.token
	}
	b.mu.Unlock()

	if !d.Ready() {
		b.Stop()
		return
	}
	if cur == d.Token {
		return
	}
	b.Stop()
	if err := b.start(d.Token); err != nil {
		b.mu.Lock()
		b.lastErr = err.Error()
		b.connected = false
		b.mu.Unlock()
		b.deps.Log("discord: 接続できませんでした: %v", err)
	}
}

func (b *Bridge) start(token string) error {
	ses, err := discordgo.New("Bot " + token)
	if err != nil {
		return err
	}
	// 本文を読むには Message Content Intent が要る。開発者ポータルで有効に
	// していないと、メンションは届いても本文が空で来る。
	ses.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
	// 断られたことをこちらで受け取り、送る間隔を自分で伸ばす。黙って
	// 待たれると、生成の経過を出す周期が乱れる。
	ses.ShouldRetryOnRateLimit = false

	api := &dgAPI{ses: ses}
	ses.AddHandler(func(_ *discordgo.Session, m *discordgo.MessageCreate) {
		b.onMessage(api, m)
	})
	ses.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		b.onInteraction(s, i)
	})
	// 参加しているサーバーごとに登録する。全体へ登録すると反映まで待たされる
	// ことがあり、入れた直後に止められないのでは緊急停止の意味がない。
	ses.AddHandler(func(s *discordgo.Session, g *discordgo.GuildCreate) {
		b.registerStop(s, g.ID)
	})

	if err := ses.Open(); err != nil {
		return err
	}

	self := ""
	if ses.State != nil && ses.State.User != nil {
		self = ses.State.User.ID
	}
	if self == "" {
		ses.Close()
		return errors.New("ボット自身の情報を取得できませんでした")
	}

	b.mu.Lock()
	b.conn = &conn{ses: ses, api: api, token: token}
	b.self = self
	b.connected = true
	b.lastErr = ""
	b.mu.Unlock()
	b.deps.Log("discord: 接続しました (%s)", self)
	return nil
}

// Stop は接続を閉じる。走っているターンはそのまま終わらせる。履歴は残るので、
// 送れなかった分は Web から読める。
func (b *Bridge) Stop() {
	b.mu.Lock()
	c := b.conn
	b.conn = nil
	b.connected = false
	b.self = ""
	b.mu.Unlock()
	if c != nil {
		_ = c.ses.Close()
	}
}

// registerStop は打ち切りのコマンドをそのサーバーへ登録する。
//
// ボットの招待に applications.commands の権限が要る。無ければここで失敗
// するので、理由をそのまま残す。
func (b *Bridge) registerStop(s *discordgo.Session, guildID string) {
	app := ""
	if s.State != nil && s.State.User != nil {
		app = s.State.User.ID
	}
	if app == "" {
		return
	}
	_, err := s.ApplicationCommandCreate(app, guildID, &discordgo.ApplicationCommand{
		Name:        StopCommand,
		Description: "このチャンネルで走っている生成を止めます",
	})
	if err != nil {
		b.deps.Log("discord: /%s を登録できませんでした (%s): %v", StopCommand, guildID, err)
	}
}

// onMessage はメンションだけを拾う。
func (b *Bridge) onMessage(api API, m *discordgo.MessageCreate) {
	// DM は非ゴール。メンションという線引きが DM では成立しない。
	if m.GuildID == "" || m.Author == nil {
		return
	}
	// 自分と他のボットは無視する。互いにメンションし合う 2 体が居ると、
	// 止まらない往復になる。
	if m.Author.Bot {
		return
	}
	self := b.selfID()
	if self == "" || !mentions(m, self) {
		return
	}
	go b.serve(context.Background(), api, incoming{
		ChannelID:  m.ChannelID,
		MessageID:  m.ID,
		AuthorID:   m.Author.ID,
		AuthorName: displayName(m),
		Content:    m.Content,
	})
}

// mentions は自分が名指しされたかを返す。@everyone には反応しない。
func mentions(m *discordgo.MessageCreate, self string) bool {
	for _, u := range m.Mentions {
		if u != nil && u.ID == self {
			return true
		}
	}
	return false
}

func displayName(m *discordgo.MessageCreate) string {
	if m.Member != nil && m.Member.Nick != "" {
		return m.Member.Nick
	}
	if m.Author.GlobalName != "" {
		return m.Author.GlobalName
	}
	return m.Author.Username
}

// onInteraction は承認のボタンと、打ち切りのコマンドを受ける。
func (b *Bridge) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != StopCommand {
			return
		}
		if b.stop(context.Background(), i.ChannelID) {
			ephemeral(s, i, "止めました。")
		} else {
			ephemeral(s, i, "このチャンネルで走っているものはありません。")
		}
		return
	}
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	kind, arg, ok := parseButton(i.MessageComponentData().CustomID)
	if !ok {
		return
	}

	presser := ""
	switch {
	case i.Member != nil && i.Member.User != nil:
		presser = i.Member.User.ID
	case i.User != nil:
		presser = i.User.ID
	}

	accepted, known := b.resolve(arg, kind == btnOK, presser)
	switch {
	case !known:
		ephemeral(s, i, "この確認は既に終わっています。")
	case !accepted:
		ephemeral(s, i, "これは呼びかけた本人だけが押せます。")
	default:
		ack(s, i)
	}
}

// ack は押されたことだけを返す。表示は次の反映で変わる。
func ack(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
}

func ephemeral(s *discordgo.Session, i *discordgo.InteractionCreate, text string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: text,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// dgAPI は discordgo を API へ合わせる薄い層。
type dgAPI struct{ ses *discordgo.Session }

func (a *dgAPI) Send(ctx context.Context, channelID, replyTo string, p Payload) (string, error) {
	msg := &discordgo.MessageSend{
		Content:    p.Content,
		Components: componentsOf(p),
	}
	if replyTo != "" {
		msg.Reference = &discordgo.MessageReference{MessageID: replyTo, ChannelID: channelID}
	}
	m, err := a.ses.ChannelMessageSendComplex(channelID, msg, discordgo.WithContext(ctx))
	if err != nil {
		return "", rated(err)
	}
	return m.ID, nil
}

func (a *dgAPI) Edit(ctx context.Context, channelID, messageID string, p Payload) error {
	comps := componentsOf(p)
	_, err := a.ses.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:         messageID,
		Channel:    channelID,
		Content:    &p.Content,
		Components: &comps,
	}, discordgo.WithContext(ctx))
	return rated(err)
}

func (a *dgAPI) Delete(ctx context.Context, channelID, messageID string) error {
	return rated(a.ses.ChannelMessageDelete(channelID, messageID, discordgo.WithContext(ctx)))
}

func (a *dgAPI) Typing(ctx context.Context, channelID string) error {
	return rated(a.ses.ChannelTyping(channelID, discordgo.WithContext(ctx)))
}

func (a *dgAPI) ChannelName(ctx context.Context, channelID string) string {
	ch, err := a.ses.Channel(channelID, discordgo.WithContext(ctx))
	if err != nil || ch == nil {
		return ""
	}
	return ch.Name
}

// rated は送りすぎの断りを、こちらの型へ写す。
func rated(err error) error {
	if err == nil {
		return nil
	}
	var rl *discordgo.RateLimitError
	if errors.As(err, &rl) && rl.RateLimit != nil {
		return &RateLimited{RetryAfter: rl.RetryAfter}
	}
	return err
}

func componentsOf(p Payload) []discordgo.MessageComponent {
	if len(p.Buttons) == 0 {
		return []discordgo.MessageComponent{}
	}
	row := discordgo.ActionsRow{}
	for _, b := range p.Buttons {
		style := discordgo.PrimaryButton
		if b.Danger {
			style = discordgo.SecondaryButton
		}
		row.Components = append(row.Components, discordgo.Button{
			CustomID: b.ID, Label: b.Label, Style: style,
		})
	}
	return []discordgo.MessageComponent{row}
}
