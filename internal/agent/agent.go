// Package agent はエージェント定義 (JSON) の読み書きを担う。
//
// 定義はファイルが正である。読み込みは常にファイルから行い、Ivis はファイルの
// 内容を超えた状態を持たない。v1 と異なるのは、書き手が Ivis にもなりうる点
// だけである (#528664)。画面から保存すると定義全体を書き直すため、手で書いた
// コメントや項目の並び順は失われる。
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// DefaultID は規定エージェントの ID。削除できず、Tier は常に 0 である。
	DefaultID = "general"
	// MinUserTier は利用者が作れる最小の Tier。入口を 1 つに保つため 0 は渡さない。
	MinUserTier = 1
)

// Colors は話し手の色として選べる名前。web/src/app.css の 8 色に対応する。
// 自由な色指定にしないのは、画面の配色と調和しない色を選べてしまうため。
var Colors = []string{"blue", "green", "amber", "violet", "teal", "rose", "orange", "indigo"}

// Agent は 1 つのエージェント定義。
type Agent struct {
	// ID はファイル名から導出する。一覧内で一意であることを読み込み時に検証する。
	ID string `json:"id"`
	// Name は表示名。
	Name string `json:"name"`
	// Description は「どういうときにこのエージェントを呼ぶか」。上位エージェントの
	// システムプロンプトにそのまま載り、委譲先を選ぶ判断材料になる。
	Description string `json:"description"`
	// Model は Ollama 上のモデル名。
	Model string `json:"model"`
	// Instructions はシステムプロンプトの中核となる指示文。
	Instructions string `json:"instructions"`
	// Tier は階層。小さいほど上位で、委譲できるのは Tier が真に大きい相手だけ。
	// 誰が誰を呼べるかがこの 1 つの数で決まるため、エージェントを増やすときに
	// 既存の定義へ手を入れる必要がない。
	Tier int `json:"tier"`
	// Tools は許可するツール名。空なら何も許可しない。"*" ですべて。
	Tools []string `json:"tools"`
	// Skills は利用可能とするスキル名。"*" ですべて。
	Skills []string `json:"skills"`
	// Memory は委譲されたときに、同じセッション内での前回のやり取りを
	// 引き継ぐか。false なら毎回まっさらなコンテキストで始まる。
	Memory bool `json:"memory"`
	// Thinking はモデルの推論機能を使うか。対応しないモデルでは失敗する。
	Thinking bool `json:"thinking"`
	// Unconfined が真のとき、ファイル操作とコマンド実行が会話の作業場所の
	// 外へ出られる。境界を丸ごと外さずここに持つのは、定義を読めば何が
	// できるか分かる状態を保つためである。委譲しても継承しない。
	Unconfined bool `json:"unconfined"`
	// Color は話し手の色。空なら ID から機械的に決める。
	Color string `json:"color,omitempty"`
	// Options は生成パラメータ (temperature, num_ctx など) をそのまま Ollama へ渡す。
	Options map[string]any `json:"options,omitempty"`

	// File は読み込み元。UI での表示と、どのファイルを直せばよいかの手がかり。
	File string `json:"file"`
	// Fixed は削除も Tier の変更もできないこと。規定エージェントだけが真。
	Fixed bool `json:"fixed"`
}

// doc はファイル上の表現。ID とファイル位置は読み込み時に導出するものなので
// 持たない。廃止した delegates は、ここに無いことで黙って無視される。手元の
// 定義が一斉に読めなくなる事態を避けるため、エラーにはしない。
type doc struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Model        string         `json:"model"`
	Instructions string         `json:"instructions"`
	Tier         *int           `json:"tier,omitempty"`
	Tools        []string       `json:"tools"`
	Skills       []string       `json:"skills"`
	Memory       bool           `json:"memory"`
	Thinking     bool           `json:"thinking"`
	Unconfined   bool           `json:"unconfined"`
	Color        string         `json:"color,omitempty"`
	Options      map[string]any `json:"options,omitempty"`
}

// LoadError は 1 つの定義の読み込み失敗。
type LoadError struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Set は読み込まれたエージェントの集合。
type Set struct {
	mu     sync.RWMutex
	agents map[string]*Agent
	order  []string
	errs   []LoadError
}

// NewSet は空の集合を返す。
func NewSet() *Set { return &Set{agents: map[string]*Agent{}} }

// Load は探索パスを順に読み、集合を入れ替える。同じ ID があれば先勝ちとし、
// 後から来たものは読み込みエラーとして記録する。
func (s *Set) Load(paths []string) {
	agents := map[string]*Agent{}
	var order []string
	var errs []LoadError

	for _, root := range paths {
		found, loadErrs := ReadDir(root)
		errs = append(errs, loadErrs...)
		for _, a := range found {
			// 共通の一覧で Tier 0 を持てるのは規定エージェントだけである。
			// 入口が 2 つある状態を、定義を手で書き換えて作れないようにする
			// ため (#528664)。会話の中の名簿にはこの制限は無い。
			if !a.Fixed && a.Tier < MinUserTier {
				a.Tier = MinUserTier
			}
			if prev, ok := agents[a.ID]; ok {
				errs = append(errs, LoadError{
					Path:   a.File,
					Reason: fmt.Sprintf("エージェント ID %q が %s と重複しています", a.ID, prev.File),
				})
				continue
			}
			agents[a.ID] = a
			order = append(order, a.ID)
		}
	}
	sortIDs(order, agents)

	s.mu.Lock()
	s.agents, s.order, s.errs = agents, order, errs
	s.mu.Unlock()
}

// ReadDir は 1 つのディレクトリの定義を ID 順に読む。読めなかったものは
// 失敗として返し、残りは通す。1 つ壊れただけで手元の定義が全部読めなくなる
// 事態を避けるためで、この扱いは共通の探索パスでもセッション固有の置き場
// (#731906) でも同じである。
//
// ディレクトリが無いことは失敗ではない。まだ誰も作っていないだけである。
func ReadDir(root string) ([]*Agent, []LoadError) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var out []*Agent
	var errs []LoadError
	for _, n := range names {
		p := filepath.Join(root, n)
		a, err := parse(p)
		if err != nil {
			errs = append(errs, LoadError{Path: p, Reason: err.Error()})
			continue
		}
		out = append(out, a)
	}
	return out, errs
}

// SortIDs は Tier 順、同じ Tier では ID 順に並べ替える。上位から下位へ読める
// 並びは、そのまま指示の流れの向きである。
func SortIDs(order []string, agents map[string]*Agent) { sortIDs(order, agents) }

// sortIDs は Tier 順、同じ Tier では ID 順に並べる。一覧も指示文へ載せる
// 並びも上位から下位へ読めるようにする。
func sortIDs(order []string, agents map[string]*Agent) {
	sort.Slice(order, func(i, j int) bool {
		a, b := agents[order[i]], agents[order[j]]
		if a.Tier != b.Tier {
			return a.Tier < b.Tier
		}
		return a.ID < b.ID
	})
}

// List は Tier 順の一覧を返す。
func (s *Set) List() []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Agent, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.agents[id])
	}
	return out
}

// Below は指定したエージェントが委譲できる相手を Tier 順で返す。
func (s *Set) Below(a *Agent) []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Agent, 0, len(s.order))
	for _, id := range s.order {
		if c := s.agents[id]; c.Tier > a.Tier {
			out = append(out, c)
		}
	}
	return out
}

// Get は ID で引く。定義が削除されていれば見つからない。セッション側は ID を
// 保持し続けるため、開くことはできるが続行はできない、という扱いになる。
func (s *Set) Get(id string) (*Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[id]
	return a, ok
}

// Errors は読み込みに失敗した定義を返す。
func (s *Set) Errors() []LoadError {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]LoadError{}, s.errs...)
}

// Allows は指定ツールの使用が許可されているかを返す。
func (a *Agent) Allows(tool string) bool {
	for _, t := range a.Tools {
		if t == "*" || t == tool {
			return true
		}
	}
	return false
}

// CanDelegateTo は相手へ委譲できるかを返す。Tier が真に大きい相手だけを呼べる。
// 同位を含めないのは、含めると同 Tier どうしで循環しうるためである。
func (a *Agent) CanDelegateTo(b *Agent) bool { return b != nil && b.Tier > a.Tier }

func parse(path string) (*Agent, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d doc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("JSON が壊れています: %v", err)
	}
	// ID はファイル名から導出する。定義内の id は無視し、ファイルの位置と
	// 識別子が常に一致する状態を保つ。
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	a := &Agent{
		ID:           id,
		Name:         d.Name,
		Description:  d.Description,
		Model:        d.Model,
		Instructions: d.Instructions,
		Tools:        d.Tools,
		Skills:       d.Skills,
		Memory:       d.Memory,
		Thinking:     d.Thinking,
		Unconfined:   d.Unconfined,
		Color:        d.Color,
		Options:      d.Options,
		File:         path,
		Fixed:        id == DefaultID,
	}

	// Tier は省略できる。書かれていなければ規定より 1 つ下として扱う。
	// 書かれていればその値をそのまま読む。0 を弾くのは共通の一覧の決まりで
	// あって、ファイルの読み方ではない — セッション固有の定義は 0 を持てる
	// (#731906)。共通の側の制限は Set.Load が掛ける。
	a.Tier = MinUserTier
	if d.Tier != nil {
		a.Tier = *d.Tier
		if a.Tier < 0 {
			a.Tier = 0
		}
	}
	// 規定エージェントの Tier はファイルに何が書いてあっても 0 とする。
	if a.Fixed {
		a.Tier = 0
	}

	if a.Name == "" {
		a.Name = a.ID
	}
	if !validColor(a.Color) {
		// 知らない色は指定が無いものとして扱う。読み込み自体は通す。
		a.Color = ""
	}
	if strings.TrimSpace(a.Model) == "" {
		return nil, fmt.Errorf("model が指定されていません")
	}
	return a, nil
}

func validColor(c string) bool {
	if c == "" {
		return true
	}
	for _, v := range Colors {
		if v == c {
			return true
		}
	}
	return false
}
