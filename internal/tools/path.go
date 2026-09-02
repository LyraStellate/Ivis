package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveInRoot は root からの相対指定を実際のパスへ解決し、境界の外に出て
// いないことを確かめる。
//
// 判定は文字列としてのパスではなく、シンボリックリンクを解決したうえでの
// 実際のパスに対して行う。文字列で判定すると、リンクを経由した境界外への
// アクセスを見逃す。
func resolveInRoot(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("パスが空です")
	}
	// Windows では "/etc/passwd" のようにドライブを伴わない指定は IsAbs が
	// false になる。そのまま結合すると別の場所を指したように見えるため、
	// 区切り文字で始まるものは絶対指定として断る。
	if filepath.IsAbs(rel) || rel[0] == '/' || rel[0] == '\\' || strings.Contains(rel, ":") {
		return "", fmt.Errorf("パスは作業ディレクトリからの相対で指定してください: %s", rel)
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// 作業ディレクトリ自体が無い場合は、解決せずそのまま基準にする。
		realRoot = filepath.Clean(root)
	}

	joined := filepath.Join(realRoot, filepath.FromSlash(rel))

	// 末端がまだ存在しないこともある (新規作成)。存在する最も深い祖先まで
	// 遡ってリンクを解決し、残りを繋ぎ直す。
	probe := joined
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	realProbe, err := filepath.EvalSymlinks(probe)
	if err != nil {
		realProbe = probe
	}
	suffix := strings.TrimPrefix(joined, probe)
	final := filepath.Clean(filepath.Join(realProbe, suffix))

	if !within(realRoot, final) {
		return "", fmt.Errorf("作業ディレクトリの外にはアクセスできません: %s", rel)
	}
	return final, nil
}

// within は p が root の内側かを判定する。
func within(root, p string) bool {
	if runtime.GOOS == "windows" {
		// Windows のパスは大文字小文字を区別しない。区別して比較すると、
		// 境界内なのに外だと誤判定する。
		root = strings.ToLower(root)
		p = strings.ToLower(p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// displayPath は root からの相対表記に戻す。利用者への提示に使う。
func displayPath(root, abs string) string {
	if rel, err := filepath.Rel(root, abs); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(abs)
}
