package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 読み書きの上限。文脈を食い潰さないための保険であって、権限の話ではない。
const (
	maxReadBytes  = 200 << 10
	maxWriteBytes = 1 << 20
)

type listDirTool struct{}

func (t *listDirTool) Name() string { return "list_dir" }
func (t *listDirTool) Description() string {
	return "作業ディレクトリ内のディレクトリ内容を一覧する。path は作業ディレクトリからの相対で指定する。"
}
func (t *listDirTool) Parameters() map[string]any {
	return schema(map[string]any{
		"path": strProp("一覧するディレクトリ。省略時は作業ディレクトリの直下。"),
	})
}
func (t *listDirTool) NeedsApproval() bool { return false }

func (t *listDirTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	rel := argStringOpt(args, "path")
	if rel == "" {
		rel = "."
	}
	abs, err := resolveInRoot(ec.Workspace, rel)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("ディレクトリを読めませんでした: %w", err)
	}

	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			lines = append(lines, name+"/")
			continue
		}
		info, err := e.Info()
		if err != nil {
			lines = append(lines, name)
			continue
		}
		lines = append(lines, fmt.Sprintf("%s (%d バイト)", name, info.Size()))
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return displayPath(ec.Workspace, abs) + " は空です。", nil
	}
	return displayPath(ec.Workspace, abs) + ":\n" + strings.Join(lines, "\n"), nil
}

type readFileTool struct{}

func (t *readFileTool) Name() string { return "read_file" }
func (t *readFileTool) Description() string {
	return "作業ディレクトリ内のファイルを読む。path は作業ディレクトリからの相対で指定する。"
}
func (t *readFileTool) Parameters() map[string]any {
	return schema(map[string]any{
		"path": strProp("読むファイル。作業ディレクトリからの相対。"),
	}, "path")
}
func (t *readFileTool) NeedsApproval() bool { return false }

func (t *readFileTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	rel, err := argString(args, "path")
	if err != nil {
		return "", err
	}
	abs, err := resolveInRoot(ec.Workspace, rel)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("ファイルを読めませんでした: %w", err)
	}
	return truncate(string(b), maxReadBytes), nil
}

type writeFileTool struct{}

func (t *writeFileTool) Name() string { return "write_file" }
func (t *writeFileTool) Description() string {
	return "作業ディレクトリ内のファイルを書く。既存の内容は置き換わる。path は作業ディレクトリからの相対で指定する。"
}
func (t *writeFileTool) Parameters() map[string]any {
	return schema(map[string]any{
		"path":    strProp("書くファイル。作業ディレクトリからの相対。"),
		"content": strProp("書き込む内容。全体を置き換える。"),
	}, "path", "content")
}

// 書き込みは元に戻せないため、承認を求める。
func (t *writeFileTool) NeedsApproval() bool { return true }

func (t *writeFileTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	rel, err := argString(args, "path")
	if err != nil {
		return "", err
	}
	content, err := argString(args, "content")
	if err != nil {
		return "", err
	}
	if len(content) > maxWriteBytes {
		return "", fmt.Errorf("書き込む内容が大きすぎます (%d バイト)", len(content))
	}
	abs, err := resolveInRoot(ec.Workspace, rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("ファイルを書けませんでした: %w", err)
	}
	return fmt.Sprintf("%s に %d バイト書き込みました。", displayPath(ec.Workspace, abs), len(content)), nil
}
