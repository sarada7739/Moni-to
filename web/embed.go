// Package web はクライアント（HTML/JS）を実行ファイルに埋め込む。
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// Static は static ディレクトリをルートとした FS を返す。
func Static() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
