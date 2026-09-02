// Package skillreg は Claude 形式のスキルを探索し、名前と説明の一覧および
// 本文の取得を提供する。
//
// スキルの形式 (SKILL.md + frontmatter) は外部と既に合意済みの契約であり、
// Ivis が独自形式を定義してよい箇所ではない。既存の Claude スキル置き場を
// そのまま探索パスに指定できることを要件とする。
package skillreg

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Skill は 1 つのスキルを表す。本文はここに持たない。毎ターンのプロンプトへ
// 載せるのは名前と説明だけで、本文はモデルが必要と判断した時点で読み込む。
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Dir はスキルのディレクトリ。同梱スクリプトの実行境界にもなる。
	Dir string `json:"dir"`
	// File は SKILL.md (または単体 md) の位置。
	File string `json:"file"`
	// Source はどの探索パスから読まれたか。衝突時の説明に使う。
	Source string `json:"source"`
}

// Conflict は同名スキルが複数の探索パスに存在したことを表す。
// 黙って一方を捨てると、利用者は編集したはずのスキルが反映されない理由に
// 辿り着けないため、記録して UI に出す。
type Conflict struct {
	Name    string `json:"name"`
	Winner  string `json:"winner"`
	Shadows string `json:"shadows"`
}

// LoadError は 1 つのスキルの読み込み失敗。全体を止めずに個別に報告する。
type LoadError struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Registry はスキルの一覧を保持する。起動時と明示的な再読込時にだけ更新する。
type Registry struct {
	mu        sync.RWMutex
	skills    map[string]*Skill
	order     []string
	conflicts []Conflict
	errs      []LoadError
}

// New は空のレジストリを返す。
func New() *Registry {
	return &Registry{skills: map[string]*Skill{}}
}

// Load は探索パスを順に読み、レジストリを入れ替える。先に指定されたパスが優先。
func (r *Registry) Load(paths []string) {
	skills := map[string]*Skill{}
	var order []string
	var conflicts []Conflict
	var errs []LoadError

	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			// 探索パスが無いのは異常ではない。設定に書いただけで未作成のことがある。
			continue
		}
		for _, f := range findSkillFiles(root, &errs) {
			s, err := parseSkill(f, root)
			if err != nil {
				errs = append(errs, LoadError{Path: f, Reason: err.Error()})
				continue
			}
			if prev, ok := skills[s.Name]; ok {
				conflicts = append(conflicts, Conflict{Name: s.Name, Winner: prev.File, Shadows: s.File})
				continue
			}
			skills[s.Name] = s
			order = append(order, s.Name)
		}
	}
	sort.Strings(order)

	r.mu.Lock()
	r.skills, r.order, r.conflicts, r.errs = skills, order, conflicts, errs
	r.mu.Unlock()
}

// List は名前順のスキル一覧を返す。
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.skills[n])
	}
	return out
}

// Get は名前でスキルを引く。
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// Conflicts は衝突の記録を返す。
func (r *Registry) Conflicts() []Conflict {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Conflict{}, r.conflicts...)
}

// Errors は読み込みに失敗したスキルを返す。
func (r *Registry) Errors() []LoadError {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]LoadError{}, r.errs...)
}

// Body はスキル本文 (frontmatter を除いた部分) を読む。
func (r *Registry) Body(name string) (string, error) {
	s, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("スキル %q は見つかりません", name)
	}
	b, err := os.ReadFile(s.File)
	if err != nil {
		return "", fmt.Errorf("スキル %q を読めませんでした: %w", name, err)
	}
	_, body := splitFrontmatter(string(b))
	return strings.TrimSpace(body), nil
}

// Filter は許可指定に一致するスキルだけを返す。要素 "*" はすべてを意味する。
func (r *Registry) Filter(allow []string) []*Skill {
	all := r.List()
	if len(allow) == 0 {
		return nil
	}
	for _, a := range allow {
		if a == "*" {
			return all
		}
	}
	want := map[string]bool{}
	for _, a := range allow {
		want[a] = true
	}
	out := make([]*Skill, 0, len(want))
	for _, s := range all {
		if want[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// findSkillFiles は探索パス直下から SKILL.md を探す。プラグイン形式のように
// 1 階層深いところに置かれることもあるため、深さ 3 までは辿る。
func findSkillFiles(root string, errs *[]LoadError) []string {
	var found []string
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			*errs = append(*errs, LoadError{Path: p, Reason: err.Error()})
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return fs.SkipDir
			}
			if strings.Count(filepath.Clean(p), string(filepath.Separator))-rootDepth > 3 {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case strings.EqualFold(d.Name(), "SKILL.md"):
			found = append(found, p)
		case strings.HasSuffix(strings.ToLower(d.Name()), ".md") && filepath.Dir(p) == filepath.Clean(root):
			// 探索パス直下に置かれた単体 md も 1 スキルとして扱う。
			found = append(found, p)
		}
		return nil
	})
	sort.Strings(found)
	return found
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func parseSkill(file, source string) (*Skill, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	head, _ := splitFrontmatter(string(b))

	var fm frontmatter
	if head != "" {
		if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
			return nil, fmt.Errorf("frontmatter を解釈できません: %v", err)
		}
	}

	dir := filepath.Dir(file)
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		// name が無いスキルは珍しくない。ディレクトリ名 (単体 md ならファイル名)
		// で代替し、読み込み自体は成功させる。
		if strings.EqualFold(filepath.Base(file), "SKILL.md") {
			name = filepath.Base(dir)
		} else {
			name = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		}
	}
	desc := strings.TrimSpace(fm.Description)
	if desc == "" {
		desc = "(説明なし)"
	}
	return &Skill{Name: name, Description: desc, Dir: dir, File: file, Source: source}, nil
}

// splitFrontmatter は先頭の --- で囲まれた領域と本文に分ける。
func splitFrontmatter(s string) (head, body string) {
	t := strings.TrimLeft(s, "\ufeff \t\r\n")
	if !strings.HasPrefix(t, "---") {
		return "", s
	}
	rest := t[3:]
	if i := strings.IndexAny(rest, "\r\n"); i >= 0 {
		rest = rest[i+1:]
	} else {
		return "", s
	}
	for _, sep := range []string{"\n---\r\n", "\n---\n", "\r\n---\r\n", "\n---"} {
		if j := strings.Index(rest, sep); j >= 0 {
			return rest[:j], rest[j+len(sep):]
		}
	}
	return "", s
}
