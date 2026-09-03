package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// Bootstrap は起動時に、最低限の定義がある状態を保つ。
//
// 何も無ければ雛形一式を書き出す。定義が 1 つも無いと何も起動できず、利用者は
// 空の画面から書式を推測することになるため。規定エージェントのファイルだけが
// 消されていた場合も既定値で作り直す。入口が失われると、どのエージェントも
// 呼べない状態になりうる。それ以外の場合は一切触らない。
func Bootstrap(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	anyJSON, hasDefault := scan(paths)
	if anyJSON && hasDefault {
		return nil
	}

	dir := paths[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeStarter(dir, DefaultID, defaultAgent()); err != nil {
		return err
	}
	if anyJSON {
		// 規定だけを補った。他の定義には触らない。
		return nil
	}
	return writeStarter(dir, "researcher", researcherAgent())
}

// scan は探索パス全体に定義があるか、規定エージェントがあるかを返す。
func scan(paths []string) (anyJSON, hasDefault bool) {
	for _, root := range paths {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
				continue
			}
			anyJSON = true
			if strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())) == DefaultID {
				hasDefault = true
			}
		}
	}
	return anyJSON, hasDefault
}

func writeStarter(dir, id string, a *Agent) error {
	a.ID = id
	a.File = filepath.Join(dir, id+".json")
	return Save(dir, a)
}

func defaultAgent() *Agent {
	return &Agent{
		Name:        "General",
		Description: "何でも受け取る入口。まず相談し、必要なら下位のエージェントへ任せる。",
		Model:       "qwen3:8b",
		Instructions: "あなたは Ivis の入口となるアシスタントです。日本語で簡潔に答えます。\n" +
			"自分で答えられることは自分で答え、専門的な作業は任せられる相手へ委譲してください。\n" +
			"ツールを使う前には、何をするかを一言添えてください。",
		Tier:    0,
		Tools:   []string{"list_dir", "read_file", "write_file", "load_skill", "run_skill_script", "delegate"},
		Skills:  []string{"*"},
		Color:   "blue",
		Options: map[string]any{"temperature": 0.7},
	}
}

func researcherAgent() *Agent {
	return &Agent{
		Name:        "Researcher",
		Description: "作業ディレクトリ内を調べたいときに呼ぶ。結論と根拠だけを返し、ファイルは書き換えない。",
		Model:       "qwen3:8b",
		Instructions: "あなたは調査担当です。与えられた依頼について作業ディレクトリ内を調べ、\n" +
			"結論と根拠だけを短くまとめて返します。ファイルは書き換えません。",
		Tier:    1,
		Tools:   []string{"list_dir", "read_file", "load_skill"},
		Skills:  []string{"*"},
		Memory:  true,
		Color:   "green",
		Options: map[string]any{"temperature": 0.3},
	}
}
