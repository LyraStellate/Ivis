package team

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/store"
)

// 会話ごとに置いていたエージェント定義を、共有のチームエージェントへ移す
// (#731906)。
//
// 前の版では、その会話のためだけの定義を <data_dir>/sessions/<ID>/agents へ
// 置いていた。チームエージェントは全てのチーム会話で共有されるようになった
// ので、置き場も 1 つになる。
//
// やり直さない印は置かない。移し終われば元のディレクトリは無くなるので、
// 処理は自然に冪等である。印を置くと、印とファイルの実際の状態が食い違った
// ときにどちらが正か決められなくなる。

// Moved は移した定義 1 件。
type Moved struct {
	SessionID string
	// From は元の ID、To は移した先の ID。名前が衝突したときだけ違う。
	From string
	To   string
}

// Result は移行の結果。
type Result struct {
	Moved []Moved
	// Sessions は定義を持っていた会話の数。
	Sessions int
}

// Renamed は名前が変わった分だけを返す。報告に出すのはこちらで、変わって
// いない移動をいちいち読ませても意味がない。
func (r Result) Renamed() []Moved {
	var out []Moved
	for _, m := range r.Moved {
		if m.From != m.To {
			out = append(out, m)
		}
	}
	return out
}

// MigrateSessionAgents は会話ごとの定義を共有の置き場へ移す。
//
// 起動時に 1 度だけ呼ぶ。定義を読み込む前でなければ、移したものがその起動で
// 見えない。
func MigrateSessionAgents(ctx context.Context, cfg *config.Config, st *store.Store) (Result, error) {
	var res Result

	root := cfg.LegacySessionsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		// 無いのがふつうである。移行済みか、そもそも作っていない。
		return res, nil
	}
	dst := cfg.TeamAgentDir()
	if dst == "" {
		return res, fmt.Errorf("チームエージェントの置き場が決まりません")
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return res, err
	}

	// 移した先で使った名前を覚える。同じ起動の中で 2 つの会話が同じ名前を
	// 持っていることは、ごく普通に起きる。
	taken := map[string]bool{}
	for _, a := range readIDs(dst) {
		taken[a] = true
	}
	for _, p := range cfg.AgentPaths {
		for _, a := range readIDs(p) {
			taken[a] = true
		}
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, sessionID := range names {
		dir := cfg.LegacySessionAgentsDir(sessionID)
		ids := readIDs(dir)
		if len(ids) == 0 {
			cleanup(dir, filepath.Join(root, sessionID))
			continue
		}
		res.Sessions++

		for _, id := range ids {
			to := freeName(id, taken)
			taken[to] = true

			if err := move(filepath.Join(dir, id+".json"), filepath.Join(dst, to+".json")); err != nil {
				return res, fmt.Errorf("%s の %s を移せませんでした: %w", sessionID, id, err)
			}
			res.Moved = append(res.Moved, Moved{SessionID: sessionID, From: id, To: to})

			if err := enable(ctx, st, sessionID, id, to); err != nil {
				return res, err
			}
		}
		cleanup(dir, filepath.Join(root, sessionID))
	}
	return res, nil
}

// enable は移した定義をその会話で有効なままにし、名前が変わったなら過去の
// 記録も直す。
//
// 記録を直さないと、@reviewer が別人になった会話で、過去の吹き出しと担当
// チケットが新しい reviewer の名前と色で描かれる。会話が「誰が何をしたか」の
// 記録として信用できなくなる (#640275)。
func enable(ctx context.Context, st *store.Store, sessionID, from, to string) error {
	sess, err := st.GetSession(ctx, sessionID)
	if err != nil {
		// データベースに無い会話のディレクトリが残っていることはある。
		// 定義は移したうえで、有効化だけを飛ばす。
		return nil
	}
	if from != to {
		if err := st.RenameAgent(ctx, sessionID, from, to); err != nil {
			return fmt.Errorf("%s の %s を %s へ書き換えられませんでした: %w",
				sessionID, from, to, err)
		}
	}
	for _, id := range sess.Members {
		if id == to {
			return nil
		}
	}
	return st.SetMembers(ctx, sessionID, append(sess.Members, to))
}

// readIDs は 1 つのディレクトリにある定義の ID を返す。
//
// 中身は解析しない。ID はファイル名から導かれるので、移すのに定義を読む
// 必要が無い。解析すると、model の無い壊れた定義が黙って取り残されて孤児に
// なる。壊れたものも移し、移した先で読み込みの失敗として見えるようにする —
// それが正しい行き先である。
func readIDs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
	}
	sort.Strings(out)
	return out
}

// freeName は空いている名前を返す。衝突したら後ろに番号を付ける。
// 番号の付け方は、画面からコピーしたときと同じにしてある。
func freeName(base string, taken map[string]bool) string {
	if !taken[base] && agent.ValidateID(base) == nil {
		return base
	}
	for i := 2; i < 1000; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if !taken[id] {
			return id
		}
	}
	return base + "-" + store.NewID()[:6]
}

// move は 1 つ移す。
//
// os.Rename が使えないことがある。data_dir と agent_paths[0] は設定で別々に
// 指せるので、別のボリュームにまたがりうる。そのときは写してから消す。
func move(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	src.Close()
	return os.Remove(from)
}

// cleanup は空になったディレクトリを片付ける。空でなければ何もしない。
// 元のディレクトリが無いこと自体が「移行済み」の記録になる。
func cleanup(dirs ...string) {
	for _, d := range dirs {
		_ = os.Remove(d)
	}
}

// Report は移行の結果を人が読む形にする。何も移していなければ空。
func Report(res Result) []string {
	if len(res.Moved) == 0 {
		return nil
	}
	out := []string{fmt.Sprintf("会話ごとのエージェントを共有のチームエージェントへ移しました  %d 件 (%d 会話)",
		len(res.Moved), res.Sessions)}
	for _, m := range res.Renamed() {
		out = append(out, fmt.Sprintf("! %s → %s (名前が重なったため改名しました)", m.From, m.To))
	}
	return out
}
