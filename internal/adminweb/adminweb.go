// Package adminweb 内嵌管理后台前端静态资源。
// 前端构建前先放置占位页面；执行 make web 后产物输出到 dist/。
package adminweb

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// Vite 配置会移除 chunk 名的前导下划线，避免 Go 的目录嵌入规则静默
// 忽略这类文件。adminweb_test 会验证所有构建引用都确实进入二进制。
//
//go:embed dist
var distFS embed.FS

// Handler 返回静态资源 handler：仅对真实文件（非目录）走文件服务，其余路径回退
// index.html 内容（SPA 深链接友好）。目录请求不再返回文件清单；带内容哈希的
// assets/ 资源可长期缓存，index.html 不缓存以便及时拿到新构建。
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
			if info, err := fs.Stat(sub, path[1:]); err == nil && !info.IsDir() {
				if strings.HasPrefix(path, "/assets/") {
					// Vite 产物文件名带内容哈希，可长期不可变缓存。
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
