package tools

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// 一覧が長くなりすぎると、それだけで文脈を食い潰す。切ったうえで
	// 総数を示し、絞り込みをやり直せるようにする。
	maxFindResults   = 200
	maxSearchResults = 100
	// 中身を探す対象の大きさの上限。これを超えるものは生成物か記録であり、
	// 探して当たっても読めない。
	maxSearchFileSize = 2 << 20
)

// skipDir はたどらないディレクトリ。中身が生成物か履歴で、探しても
// 目当てのものが出てこないうえに件数だけが膨らむ。
var skipDir = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"__pycache__": true, ".venv": true, "dist": true, "build": true,
}

type findFilesTool struct{}

func (t *findFilesTool) Name() string { return "find_files" }
func (t *findFilesTool) Description() string {
	return "名前の形でファイルを探す。pattern は *.go や cmd/**/main.go のように書く。" +
		"一覧と読み取りだけで目当てを探すと往復が増えるので、まずこれを使う。"
}
func (t *findFilesTool) Parameters() map[string]any {
	return schema(map[string]any{
		"pattern": strProp("探す名前の形。* と ? が使える。区切りを含めると相対パス全体に対して照合する。"),
		"path":    strProp("探し始める場所。省略すると作業ディレクトリ。"),
	}, "pattern")
}
func (t *findFilesTool) NeedsApproval() bool { return false }

func (t *findFilesTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	pattern, err := argString(args, "pattern")
	if err != nil {
		return "", err
	}
	root, err := searchRoot(ec, argStringOpt(args, "path"))
	if err != nil {
		return "", err
	}

	var hits []string
	total := 0
	err = walk(ctx, root, func(path string, d fs.DirEntry) error {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		target := rel
		if !strings.ContainsAny(pattern, "/") {
			target = d.Name()
		}
		ok, _ := matchPath(pattern, target)
		if !ok {
			return nil
		}
		total++
		if len(hits) < maxFindResults {
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if total == 0 {
		return fmt.Sprintf("%s の下に %q に一致するファイルはありません。", displayPath(ec.Workspace, root), pattern), nil
	}
	out := strings.Join(hits, "\n")
	if total > len(hits) {
		out += fmt.Sprintf("\n\n... 全 %d 件のうち %d 件を表示しました。pattern を絞ってください。", total, len(hits))
	}
	return out, nil
}

// matchPath は ** を含む形にも答える。filepath.Match は ** を階層をまたぐ
// 意味に取らないため、そこだけ自前で開く。
func matchPath(pattern, target string) (bool, error) {
	if !strings.Contains(pattern, "**") {
		return filepath.Match(pattern, target)
	}
	// "a/**/b" は "a/b" にも当たるべきである。両方の形を試す。
	flat := strings.ReplaceAll(pattern, "/**/", "/")
	if ok, _ := filepath.Match(flat, target); ok {
		return true, nil
	}
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\*\*`, ".*")
	re = strings.ReplaceAll(re, `\*`, "[^/]*")
	re = strings.ReplaceAll(re, `\?`, "[^/]")
	m, err := regexp.Compile("^" + re + "$")
	if err != nil {
		return false, nil
	}
	return m.MatchString(target), nil
}

type searchTextTool struct{}

func (t *searchTextTool) Name() string { return "search_text" }
func (t *searchTextTool) Description() string {
	return "ファイルの中身を正規表現で探し、当たった行を返す。どのファイルに書いてあるか分からないときに使う。"
}
func (t *searchTextTool) Parameters() map[string]any {
	return schema(map[string]any{
		"pattern": strProp("探す正規表現。"),
		"path":    strProp("探し始める場所。省略すると作業ディレクトリ。"),
		"glob":    strProp("対象を名前で絞る形。例: *.go"),
	}, "pattern")
}
func (t *searchTextTool) NeedsApproval() bool { return false }

func (t *searchTextTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	pattern, err := argString(args, "pattern")
	if err != nil {
		return "", err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("正規表現が誤っています: %v", err)
	}
	root, err := searchRoot(ec, argStringOpt(args, "path"))
	if err != nil {
		return "", err
	}
	glob := argStringOpt(args, "glob")

	var lines []string
	total := 0
	err = walk(ctx, root, func(path string, d fs.DirEntry) error {
		if glob != "" {
			if ok, _ := matchPath(glob, d.Name()); !ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxSearchFileSize {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(b[:min(len(b), 8<<10)], 0) >= 0 {
			// 読めないものと、中身が文字でないものは飛ばす。
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for i, line := range strings.Split(string(b), "\n") {
			if !re.MatchString(line) {
				continue
			}
			total++
			if len(lines) < maxSearchResults {
				lines = append(lines, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimRight(line, "\r")))
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if total == 0 {
		return fmt.Sprintf("%s の下に %q に一致する行はありません。", displayPath(ec.Workspace, root), pattern), nil
	}
	out := strings.Join(lines, "\n")
	if total > len(lines) {
		out += fmt.Sprintf("\n\n... 全 %d 件のうち %d 件を表示しました。pattern か glob を絞ってください。", total, len(lines))
	}
	return out, nil
}

// searchRoot は探し始める場所を解決する。
func searchRoot(ec *ExecContext, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return ec.Workspace, nil
	}
	return resolve(ec.Workspace, rel, ec.Confined)
}

// walk は木をたどり、ファイルごとに fn を呼ぶ。中断は ctx で効く。
func walk(ctx context.Context, root string, fn func(path string, d fs.DirEntry) error) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// 読めない場所があっても探索そのものは続ける。1 つの権限不足で
			// 全体が失敗すると、探しものが見つからない理由が分からない。
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && (skipDir[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		return fn(path, d)
	})
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("探索を中断しました")
		}
		return fmt.Errorf("%s を探索できませんでした: %v", root, err)
	}
	return nil
}
