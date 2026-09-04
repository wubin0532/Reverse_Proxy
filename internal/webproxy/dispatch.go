package webproxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"luckyx/internal/config"
)

// siteHandler 站点入口：规则匹配 → 安全检查 → 按类型分发，并记录访问日志。
type siteHandler struct {
	ss *siteServer
}

func (h *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	ruleName := "-"
	defer func() {
		h.ss.logs.Add(fmt.Sprintf("%s %s %s%s 规则[%s] %d %dms",
			clientIP(r), r.Method, r.Host, r.URL.RequestURI(), ruleName,
			sw.status, time.Since(start).Milliseconds()))
	}()

	rule := matchRule(h.ss.site.Rules, r.Host, r.URL.Path)
	if rule == nil {
		writeNotFound(sw, r)
		return
	}
	ruleName = rule.Name

	if !checkRuleGuard(sw, r, rule, h.ss.logs) {
		return
	}

	switch rule.Type {
	case "reverse":
		rh, err := h.ss.reverseHandlerFor(rule)
		if err != nil {
			h.ss.logs.Add(fmt.Sprintf("%s 规则[%s] 反代不可用: %v", clientIP(r), rule.Name, err))
			http.Error(sw, "502 Bad Gateway", http.StatusBadGateway)
			return
		}
		rh.ServeHTTP(sw, r)
	case "redirect":
		redirectHandler(*rule).ServeHTTP(sw, r)
	case "fileserver":
		fileServerHandler(*rule).ServeHTTP(sw, r)
	default:
		writeNotFound(sw, r)
	}
}

// matchRule 匹配子规则：仅 Enabled；先按 FrontendHost 过滤（空=任意，
// 否则精确匹配，忽略大小写与端口），再按 FrontendPath 最长前缀优先，同级取先定义的。
func matchRule(rules []config.SubRule, host, path string) *config.SubRule {
	host = hostOnly(host)
	var best *config.SubRule
	bestLen := -1
	for i := range rules {
		rule := &rules[i]
		if !rule.Enabled {
			continue
		}
		if rule.FrontendHost != "" && !strings.EqualFold(hostOnly(rule.FrontendHost), host) {
			continue
		}
		prefix := rule.FrontendPath
		if prefix == "" {
			prefix = "/"
		}
		if !strings.HasPrefix(prefix, "/") {
			prefix = "/" + prefix
		}
		if !pathMatch(prefix, path) {
			continue
		}
		if len(prefix) > bestLen {
			best = rule
			bestLen = len(prefix)
		}
	}
	return best
}

// pathMatch 路径前缀匹配，带边界判断："/api" 可匹配 "/api" 与 "/api/x"，不匹配 "/apis"。
func pathMatch(prefix, path string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	return prefix[len(prefix)-1] == '/' || path[len(prefix)] == '/'
}

// hostOnly 去掉主机名中的端口。
func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// clientIP 取客户端 IP：优先 X-Forwarded-For 首跳，否则 RemoteAddr。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return hostOnly(r.RemoteAddr)
}

// statusWriter 包装 ResponseWriter 以记录状态码，同时透传 Flush/Hijack，
// 保证 WebSocket 升级与流式响应不受影响。
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("底层连接不支持 Hijack")
	}
	return hj.Hijack()
}

// writeNotFound 无匹配子规则时的 404 提示页。
func writeNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>404</title></head>
<body style="font-family:sans-serif;text-align:center;padding:60px 20px">
<h1>404</h1><p>没有匹配该请求的子规则</p><hr style="width:240px"><p>luckyx</p>
</body></html>`)
}
