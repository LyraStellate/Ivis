package tools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type loadSkillTool struct{}

func (t *loadSkillTool) Name() string { return "load_skill" }
func (t *loadSkillTool) Description() string {
	return "スキルの本文を読み込む。利用可能なスキルの一覧は指示文に示されている。手順が必要になった時点で呼ぶこと。"
}
func (t *loadSkillTool) Parameters() map[string]any {
	return schema(map[string]any{
		"name": strProp("読み込むスキルの名前。"),
	}, "name")
}
func (t *loadSkillTool) NeedsApproval() bool { return false }

func (t *loadSkillTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	name, err := argString(args, "name")
	if err != nil {
		return "", err
	}
	body, err := ec.Skills.Body(name)
	if err != nil {
		return "", err
	}
	s, _ := ec.Skills.Get(name)
	header := fmt.Sprintf("スキル %q の本文です。このスキルのディレクトリは %s です。\n\n", name, s.Dir)
	return header + truncate(body, maxReadBytes), nil
}

type runSkillScriptTool struct{}

func (t *runSkillScriptTool) Name() string { return "run_skill_script" }
func (t *runSkillScriptTool) Description() string {
	return "スキルに同梱されたスクリプトを実行する。script はそのスキルのディレクトリからの相対で指定する。作業ディレクトリで実行される。"
}
func (t *runSkillScriptTool) Parameters() map[string]any {
	return schema(map[string]any{
		"skill":  strProp("スクリプトを持つスキルの名前。"),
		"script": strProp("実行するスクリプト。スキルのディレクトリからの相対。"),
		"args":   strProp("スクリプトへ渡す引数。空白区切り。省略可。"),
	}, "skill", "script")
}

// スクリプトの実行は外部へ副作用を及ぼしうるため、承認を求める。
func (t *runSkillScriptTool) NeedsApproval() bool { return true }

func (t *runSkillScriptTool) Execute(ctx context.Context, ec *ExecContext, args map[string]any) (string, error) {
	skillName, err := argString(args, "skill")
	if err != nil {
		return "", err
	}
	rel, err := argString(args, "script")
	if err != nil {
		return "", err
	}
	s, ok := ec.Skills.Get(skillName)
	if !ok {
		return "", fmt.Errorf("スキル %q は見つかりません", skillName)
	}

	// スクリプトはスキルのディレクトリの内側に限る。ファイル操作と同じ理由で、
	// 実際のパスに解決したうえで境界を確かめる。
	abs, err := resolveInRoot(s.Dir, rel)
	if err != nil {
		return "", fmt.Errorf("スキル %q の外は実行できません: %w", skillName, err)
	}

	ctx, cancel := context.WithTimeout(ctx, ec.ScriptTimeout)
	defer cancel()

	name, argv := interpreter(abs)
	argv = append(argv, strings.Fields(argStringOpt(args, "args"))...)

	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Dir = ec.Workspace
	out, runErr := cmd.CombinedOutput()

	result := truncate(strings.TrimSpace(decodeConsole(out)), 32<<10)
	if ctx.Err() != nil {
		return "", fmt.Errorf("スクリプトが %s を超えたため打ち切りました:\n%s", ec.ScriptTimeout, result)
	}
	if runErr != nil {
		// 失敗もモデルにとっては情報である。こちらで打ち切らず、内容を返して
		// 自己修正の機会を与える。
		return fmt.Sprintf("スクリプトは失敗しました (%v)。出力:\n%s", runErr, result), nil
	}
	if result == "" {
		result = "(出力はありません)"
	}
	return result, nil
}

// interpreter は拡張子から実行方法を決める。
func interpreter(path string) (string, []string) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return "python", []string{path}
	case ".js", ".mjs":
		return "node", []string{path}
	case ".sh":
		return "bash", []string{path}
	case ".ps1":
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-File", path}
	default:
		return path, nil
	}
}
