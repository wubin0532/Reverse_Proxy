// Package adminweb 内嵌管理后台前端静态资源。
// 前端构建前先放置占位页面；执行 make web 后产物输出到 dist/。
package adminweb

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"time"
)

//go:embed dist
var distFS embed.FS

// Handler 返回静态资源 handler，未匹配路径直接返回 index.html 内容（SPA 深链接友好）。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, path[1:]); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
