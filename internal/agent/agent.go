// Package agent はエージェント定義 (JSON) の読み込みを担う。
//
// 定義はファイルが正であり、Ivis 側から書き戻さない。利用者が手で編集し、
// git で管理し、他人と共有する対象だからである。例外は初回起動時の雛形出力のみ。
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

// Agent は 1 つのエージェント定義。
type Agent struct {
	// ID はファイル名から導出する。一覧内で一意であることを読み込み時に検証する。
	ID string `json:"id"`
	// Name は表示名。
	Name string `json:"name"`
	// Description は一覧および委譲先の選択でモデルに示す説明。
	Description string `json:"description"`
	// Model は Ollama 上のモデル名。
	Model string `json:"model"`
	// Instructions はシステムプロンプトの中核となる指示文。
	Instructions string `json:"instructions"`
	// Tools は許可するツール名。空なら何も許可しない。"*" ですべて。
	Tools []string `json:"tools"`
	// Skills は利用可能とするスキル名。"*" ですべて。
	Skills []string `json:"skills"`
	// Delegates は委譲先として呼べる子エージェントの ID。ここに無い相手は呼べない。
	Delegates []string `json:"delegates"`
	// Options は生成パラメータ (temperature, num_ctx など) をそのまま Ollama へ渡す。
	Options map[string]any `json:"options,omitempty"`

	// File は読み込み元。UI での表示と、どのファイルを直せばよいかの手がかり。
	File string `json:"file"`
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
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // 未作成の探索パスは異常ではない
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)

		for _, n := range names {
			p := filepath.Join(root, n)
			a, err := parse(p)
			if err != nil {
				errs = append(errs, LoadError{Path: p, Reason: err.Error()})
				continue
			}
			if prev, ok := agents[a.ID]; ok {
				errs = append(errs, LoadError{
					Path:   p,
					Reason: fmt.Sprintf("エージェント ID %q が %s と重複しています", a.ID, prev.File),
				})
				continue
			}
			agents[a.ID] = a
			order = append(order, a.ID)
		}
	}
	sort.Strings(order)

	s.mu.Lock()
	s.agents, s.order, s.errs = agents, order, errs
	s.mu.Unlock()
}

// List は ID 順の一覧を返す。
func (s *Set) List() []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Agent, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.agents[id])
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

// CanDelegateTo は指定エージェントへの委譲が許可されているかを返す。
func (a *Agent) CanDelegateTo(id string) bool {
	for _, d := range a.Delegates {
		if d == "*" || d == id {
			return true
		}
	}
	return false
}

func parse(path string) (*Agent, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Agent
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("JSON が壊れています: %v", err)
	}
	// ID はファイル名から導出する。定義内の id は無視し、ファイルの位置と
	// 識別子が常に一致する状態を保つ。
	a.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	a.File = path

	if a.Name == "" {
		a.Name = a.ID
	}
	if strings.TrimSpace(a.Model) == "" {
		return nil, fmt.Errorf("model が指定されていません")
	}
	return &a, nil
}

// WriteStarter は初回起動時に限り、エージェントの雛形を書き出す。
// 1 つも定義が無い状態では何も起動できないため、出発点だけ用意する。
func WriteStarter(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			return nil // 既に何かある。触らない。
		}
	}

	// 出力用の型を別に持つ。ID とファイル位置は読み込み時に導出するものなので、
	// 雛形ファイルには書かない。
	type starterDoc struct {
		Name         string         `json:"name"`
		Description  string         `json:"description"`
		Model        string         `json:"model"`
		Instructions string         `json:"instructions"`
		Tools        []string       `json:"tools"`
		Skills       []string       `json:"skills"`
		Delegates    []string       `json:"delegates,omitempty"`
		Options      map[string]any `json:"options,omitempty"`
	}

	starters := map[string]starterDoc{
		"general": {
			Name:         "General",
			Description:  "汎用の対話エージェント。調べ物や下書きを頼む相手。",
			Model:        "qwen3:8b",
			Instructions: "あなたは Ivis の汎用アシスタントです。日本語で簡潔に答えます。\n必要なときだけツールを使い、使う前に何をするかを一言添えてください。",
			Tools:        []string{"list_dir", "read_file", "write_file", "load_skill", "run_skill_script", "delegate"},
			Skills:       []string{"*"},
			Delegates:    []string{"researcher"},
			Options:      map[string]any{"temperature": 0.7},
		},
		"researcher": {
			Name:         "Researcher",
			Description:  "作業ディレクトリ内を読んで調べ、要点だけを返す。書き込みはしない。",
			Model:        "qwen3:8b",
			Instructions: "あなたは調査担当です。与えられた依頼について作業ディレクトリ内を調べ、\n結論と根拠だけを短くまとめて返します。ファイルは書き換えません。",
			Tools:        []string{"list_dir", "read_file", "load_skill"},
			Skills:       []string{"*"},
			Options:      map[string]any{"temperature": 0.3},
		},
	}

	for id, a := range starters {
		b, err := json.MarshalIndent(a, "", "  ")
		if err != nil {
			return err
		}
		p := filepath.Join(dir, id+".json")
		if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}
