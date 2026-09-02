package httpapi

import (
	"io/fs"
	"net/http"
	"strings"
)

// staticHandler は埋め込まれたフロントエンドを配信する。
//
// 未ビルドの状態でも起動だけはできるようにし、何をすればよいかを画面に出す。
// 起動が失敗するより、原因が読める画面が出るほうが立て直しやすい。
func (s *Server) staticHandler() http.Handler {
	if s.assets == nil {
		return http.HandlerFunc(notBuilt)
	}
	if _, err := fs.Stat(s.assets, "index.html"); err != nil {
		return http.HandlerFunc(notBuilt)
	}

	files := http.FileServer(http.FS(s.assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.assets, p); err != nil {
			// 画面遷移は前面で処理するため、未知のパスは index.html を返す。
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}

const notBuiltPage = `<!doctype html>
<html lang="ja"><head><meta charset="utf-8"><title>Ivis</title>
<style>
body{font-family:system-ui,sans-serif;margin:0;display:grid;place-items:center;
min-height:100vh;background:#14161a;color:#e6e8ec}
main{max-width:34rem;padding:2rem;line-height:1.8}
code{background:#22252b;padding:.15em .4em;border-radius:4px}
h1{font-size:1.3rem}
</style></head><body><main>
<h1>フロントエンドが未ビルドです</h1>
<p>サーバーは動いています。画面を出すにはフロントエンドをビルドしてください。</p>
<pre><code>cd web
npm install
npm run build</code></pre>
<p>ビルド後、Ivis を再ビルドして起動し直すとこの画面は置き換わります。
API は <code>/api/status</code> などで既に応答しています。</p>
</main></body></html>`

func notBuilt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(notBuiltPage))
}
