package webproxy

import (
	"net/http"
	"strings"

	"andey-proxy/internal/config"
)

// redirectHandler 按规则返回重定向。
// 支持 {path}（请求路径）与 {query}（原始查询串）占位符替换；
// 状态码仅接受 301/302/307/308，其余按 302 处理。
func redirectHandler(rule config.SubRule) http.Handler {
	code := rule.RedirectCode
	switch code {
	case http.StatusMovedPermanently, http.StatusFound,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		code = http.StatusFound
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := rule.RedirectURL
		target = strings.ReplaceAll(target, "{path}", r.URL.Path)
		target = strings.ReplaceAll(target, "{query}", r.URL.RawQuery)
		http.Redirect(w, r, target, code)
	})
}
