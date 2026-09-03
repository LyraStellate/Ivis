package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FieldError は保存を断った理由を、どの入力欄の問題かとともに示す。まとめて
// 「保存できません」にすると、利用者はどこを直せばよいか分からない。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *FieldError) Error() string { return e.Reason }

// ErrFixed は規定エージェントに対して許されない操作を試みたこと。
var ErrFixed = errors.New("規定エージェントに対しては行えません")

// ValidateID は ID として使える文字列かを返す。ID はそのままファイル名に
// なるため、区切り文字や親ディレクトリ参照を含むものは断る。
func ValidateID(id string) error {
	if id == "" {
		return &FieldError{Field: "id", Reason: "ID を入力してください"}
	}
	if len(id) > 64 {
		return &FieldError{Field: "id", Reason: "ID は 64 文字までです"}
	}
	if strings.HasPrefix(id, ".") {
		return &FieldError{Field: "id", Reason: "ID を . で始めることはできません"}
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return &FieldError{Field: "id",
				Reason: "ID に使えるのは英数字と - _ . だけです"}
		}
	}
	return nil
}

// Validate は保存してよい定義かを返す。
func Validate(a *Agent) error {
	if err := ValidateID(a.ID); err != nil {
		return err
	}
	if strings.TrimSpace(a.Model) == "" {
		return &FieldError{Field: "model", Reason: "モデルを選んでください"}
	}
	if a.ID == DefaultID {
		if a.Tier != 0 {
			return &FieldError{Field: "tier",
				Reason: "規定エージェントの Tier は 0 で固定です"}
		}
	} else if a.Tier < MinUserTier {
		return &FieldError{Field: "tier",
			Reason: fmt.Sprintf("Tier は %d 以上を指定してください。0 は規定エージェントだけが持てます", MinUserTier)}
	}
	if !validColor(a.Color) {
		return &FieldError{Field: "color", Reason: "選べない色です"}
	}
	return nil
}

// Save は定義をファイルへ書き出す。a.File が空なら dir の下へ新しく作る。
//
// 一時ファイルへ書いてから置き換えるのは、書き込みの途中で落ちたときに
// 壊れた JSON が残らないようにするためである。
func Save(dir string, a *Agent) error {
	if err := Validate(a); err != nil {
		return err
	}
	path := a.File
	if path == "" {
		if dir == "" {
			return errors.New("エージェントの保存先が設定されていません")
		}
		path = filepath.Join(dir, a.ID+".json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	b, err := json.MarshalIndent(a.toDoc(), "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".ivis-agent-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 置き換えに成功していれば消すものは無い
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	a.File = path
	return nil
}

// Delete は定義ファイルを消す。規定エージェントは断る。
func Delete(a *Agent) error {
	if a.ID == DefaultID {
		return fmt.Errorf("規定エージェントは削除できません")
	}
	if a.File == "" {
		return fmt.Errorf("エージェント %q の読み込み元が分かりません", a.ID)
	}
	return os.Remove(a.File)
}

func (a *Agent) toDoc() doc {
	tier := a.Tier
	return doc{
		Name:         a.Name,
		Description:  a.Description,
		Model:        a.Model,
		Instructions: a.Instructions,
		Tier:         &tier,
		Tools:        nonNil(a.Tools),
		Skills:       nonNil(a.Skills),
		Memory:       a.Memory,
		Thinking:     a.Thinking,
		Color:        a.Color,
		Options:      a.Options,
	}
}

// nonNil は null ではなく空配列として書き出させる。手で開いたときに、
// 項目が存在すること自体は見えるようにするため。
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
