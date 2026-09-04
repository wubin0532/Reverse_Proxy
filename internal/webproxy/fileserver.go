package webproxy

import (
	"net/http"

	"andey-proxy/internal/config"
)

// fileServerHandler 静态文件服务：http.FileServer + http.Dir。
// http.Dir 内部会做路径清理并拒绝越出根目录的请求（防目录穿越）；
// 目录请求默认展示列表（Go 内置行为）。
// FrontendPath 非 "/" 时先 strip 前缀再映射到根目录。
func fileServerHandler(rule config.SubRule) http.Handler {
	fs := http.FileServer(http.Dir(rule.RootDir))
	prefix := rule.FrontendPath
	if prefix == "" || prefix == "/" {
		return fs
	}
	return http.StripPrefix(prefix, fs)
}
