package discord

import (
	"context"

	"github.com/LyraStellate/Ivis/internal/command"
	"github.com/LyraStellate/Ivis/internal/store"
)

// コマンドの表そのものは internal/command にある。Web からも同じ表を引く
// ためで、入口ごとに表を持つと、片方だけ増えた状態が必ず生まれる (#486237)。
//
// ここに残すのは、Discord での会話の引き方だけである。Discord はチャンネル
// から会話を引き、Web は開いている会話をそのまま渡す。

// runCommand はコマンドを実行し、返す文を返す。
func (b *Bridge) runCommand(ctx context.Context, in incoming, name, arg string) string {
	var sess *store.Session
	if s, err := b.deps.Store.SessionByChannel(ctx, in.ChannelID); err == nil {
		sess = s
	}
	return command.Run(ctx, b.commandDeps(), sess, name, arg)
}

// commandDeps はコマンドが使うもの。圧縮は 1 ターンの実行と同じ経路
// (deps.Compact) から来る。
func (b *Bridge) commandDeps() *command.Deps {
	return &command.Deps{Store: b.deps.Store, Runs: b.deps.Runs, Compact: b.deps.Compact}
}
