package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type editFileTool struct{}

func (t *editFileTool) Name() string { return "edit_file" }
func (t *editFileTool) Description() string {
	return "ファイルの中の文字列を差し替える。全文を書き直さずに済むので、大きなファイルはこちらを使う。" +
		"old は前後を含めてファイル内で 1 か所に定まるように書くこと。"
}
func (t *editFileTool) Parameters() map[string]any {
	return schema(map[string]any{
		"path": strProp("対象のファイル。"),
		"old":  strProp("差し替える前の文字列。ファイル内で 1 か所だけに一致する必要がある。"),
		"new":  strProp("差し替えた後の文字列。空にすると削除になる。"),
	}, "path", "old", "new")
}
func (t *editFileTool) NeedsApproval() bool { return true }

func (t *editFileTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	rel, err := argString(args, "path")
	if err != nil {
		return "", err
	}
	old, err := argString(args, "old")
	if err != nil {
		return "", err
	}
	if old == "" {
		return "", fmt.Errorf("old が空です。差し替える場所が決まりません")
	}
	next := argStringOpt(args, "new")

	abs, err := resolve(ec.Workspace, rel, ec.Confined)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("読み込めませんでした: %v", err)
	}
	body := string(b)

	// 一致が定まらないまま書き換えると、モデルは直したつもりで別の場所を
	// 壊す。件数を返して、指定をやり直せるようにする。
	n := strings.Count(body, old)
	switch {
	case n == 0:
		return "", fmt.Errorf("old がファイル内に見つかりません。空白や改行まで一致している必要があります")
	case n > 1:
		return "", fmt.Errorf("old が %d か所に一致します。前後を含めて 1 か所に定まるように書き直してください", n)
	}

	if err := os.WriteFile(abs, []byte(strings.Replace(body, old, next, 1)), 0o644); err != nil {
		return "", fmt.Errorf("書き込めませんでした: %v", err)
	}
	return fmt.Sprintf("%s の 1 か所を差し替えました。", displayPath(ec.Workspace, abs)), nil
}

type moveFileTool struct{}

func (t *moveFileTool) Name() string { return "move_file" }
func (t *moveFileTool) Description() string {
	return "ファイルやディレクトリを動かす。名前の変更にも使う。行き先が既にある場合は断る。"
}
func (t *moveFileTool) Parameters() map[string]any {
	return schema(map[string]any{
		"from": strProp("動かすもの。"),
		"to":   strProp("行き先。"),
	}, "from", "to")
}
func (t *moveFileTool) NeedsApproval() bool { return true }

func (t *moveFileTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	fromRel, err := argString(args, "from")
	if err != nil {
		return "", err
	}
	toRel, err := argString(args, "to")
	if err != nil {
		return "", err
	}
	from, err := resolve(ec.Workspace, fromRel, ec.Confined)
	if err != nil {
		return "", err
	}
	to, err := resolve(ec.Workspace, toRel, ec.Confined)
	if err != nil {
		return "", err
	}
	// 上書きを断るのは、動かした結果として何かが消えたことに気づけないため。
	if _, err := os.Lstat(to); err == nil {
		return "", fmt.Errorf("%s は既にあります", displayPath(ec.Workspace, to))
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		return "", fmt.Errorf("動かせませんでした: %v", err)
	}
	return fmt.Sprintf("%s を %s へ動かしました。",
		displayPath(ec.Workspace, from), displayPath(ec.Workspace, to)), nil
}

type deleteFileTool struct{}

func (t *deleteFileTool) Name() string { return "delete_file" }
func (t *deleteFileTool) Description() string {
	return "ファイルを消す。ディレクトリを中身ごと消すときは recursive を真にする。元には戻せない。"
}
func (t *deleteFileTool) Parameters() map[string]any {
	return schema(map[string]any{
		"path": strProp("消すもの。"),
		"recursive": map[string]any{
			"type":        "boolean",
			"description": "ディレクトリを中身ごと消す。既定は偽。",
		},
	}, "path")
}
func (t *deleteFileTool) NeedsApproval() bool { return true }

func (t *deleteFileTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	rel, err := argString(args, "path")
	if err != nil {
		return "", err
	}
	abs, err := resolve(ec.Workspace, rel, ec.Confined)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("見つかりません: %s", displayPath(ec.Workspace, abs))
	}

	// ディレクトリを中身ごと消すのは、明示的に頼まれたときだけにする。
	// 引数の取り違えで木が丸ごと消えるのが最も取り返しがつかない。
	if info.IsDir() {
		if !argBool(args, "recursive") {
			return "", fmt.Errorf("%s はディレクトリです。中身ごと消すなら recursive を真にしてください",
				displayPath(ec.Workspace, abs))
		}
		if err := os.RemoveAll(abs); err != nil {
			return "", fmt.Errorf("消せませんでした: %v", err)
		}
		return fmt.Sprintf("%s を中身ごと消しました。", displayPath(ec.Workspace, abs)), nil
	}
	if err := os.Remove(abs); err != nil {
		return "", fmt.Errorf("消せませんでした: %v", err)
	}
	return fmt.Sprintf("%s を消しました。", displayPath(ec.Workspace, abs)), nil
}
