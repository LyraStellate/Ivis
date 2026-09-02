// Package web はビルド済みのフロントエンドをバイナリへ埋め込む。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets はビルド成果物のファイルシステムを返す。未ビルドなら nil を返し、
// 呼び出し側が案内画面に切り替える。
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
