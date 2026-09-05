package webproxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

// siteHandler 站点入口：规则匹配 → 安全检查 → 按类型分发，并记录访问日志。
type siteHandler struct {
	ss *siteServer
}

func (h *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK, ss: h.ss}
	ruleID := h.ss.siteSnapshot().ID
	defer func() {
		path := r.URL.EscapedPath()
		if path == "" {
			path = "/"
		}
		message := fmt.Sprintf("%s %s %d %dms", clientIP(r), path, sw.status, time.Since(start).Milliseconds())
		h.ss.logs.Add(message)
		level := "info"
		if sw.status >= 500 {
			level = "error"
		} else if sw.status >= 400 {
			level = "warn"
		}
		logcenter.Add("webproxy", ruleID, clientIP(r), level, message)
		// 站点级流量统计：入字节取 ContentLength（未知 -1 记 0），出字节由 statusWriter 累计。
		// Hijack（WebSocket）后的流量绕过 ResponseWriter，不计入。
		bytesIn := r.ContentLength
		if bytesIn < 0 {
			bytesIn = 0
		}
		h.ss.addStats(sw.status, bytesIn, sw.bytes)
	}()

	site := h.ss.siteSnapshot()
	rule := matchRule(site.Rules, r.Host, r.URL.Path)
	if rule == nil {
		writeNotFound(sw, r)
		return
	}
	ruleID = rule.ID
	if allowed, retryAfter := h.ss.limiter.allow(rule, clientIP(r), time.Now()); !allowed {
		w.Header().Set("Retry-After", fmt.Sprint(retryAfter))
		http.Error(sw, "429 Too Many Requests", http.StatusTooManyRequests)
		return
	}

	// 强制 HTTPS：监听层嗅探分流出的明文连接（r.TLS == nil）301 跳转到 https，
	// 同端口的 TLS 请求继续正常分发，避免循环跳转。
	if r.TLS == nil && forceHTTPSActive(site) {
		target, ok := forceHTTPSRedirectTarget(r)
		if !ok {
			http.Error(sw, "400 Bad Request", http.StatusBadRequest)
			return
		}
		http.Redirect(sw, r, target, http.StatusMovedPermanently)
		return
	}

	if !checkRuleGuard(sw, r, rule, h.ss.logs) {
		return
	}

	switch rule.Type {
	case "reverse":
		if rule.MaxRequestBodyMiB > 0 {
			limit := int64(rule.MaxRequestBodyMiB) << 20
			if r.ContentLength > limit {
				http.Error(sw, "413 Request Entity Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(sw, r.Body, limit)
		}
		rh, err := h.ss.reverseHandlerFor(rule)
		if err != nil {
			h.ss.logs.Add(fmt.Sprintf("%s 规则[%s] 反代不可用: %v", clientIP(r), rule.Name, err))
			http.Error(sw, "502 Bad Gateway", http.StatusBadGateway)
			return
		}
		rh.ServeHTTP(sw, r)
	case "redirect", "fileserver":
		h.ss.staticHandlerFor(rule).ServeHTTP(sw, r)
	default:
		writeNotFound(sw, r)
	}
}

// staticHandlerFor 取（或惰性构建并缓存）redirect/fileserver 规则的处理器，
// 避免每请求重建。缓存随站点重启与 updateSite 热更新一起失效；
// 两类处理器均无内部状态，无需额外清理。
func (ss *siteServer) staticHandlerFor(rule *config.SubRule) http.Handler {
	ss.handlerMu.Lock()
	defer ss.handlerMu.Unlock()
	if ss.staticHandler == nil { // 兼容测试直接构造的 siteServer
		ss.staticHandler = make(map[string]http.Handler)
	}
	if h, ok := ss.staticHandler[rule.ID]; ok {
		return h
	}
	var handler http.Handler
	if rule.Type == "redirect" {
		handler = redirectHandler(*rule)
	} else {
		handler = fileServerHandler(*rule)
	}
	ss.staticHandler[rule.ID] = handler
	return handler
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

// forceHTTPSActive 站点启用强制 HTTPS 且已绑定 ACME 证书
// （CertID 为空时只能自签回退，不视为有效证书，不启用嗅探与跳转）。
func forceHTTPSActive(site config.Site) bool {
	return site.TLS && site.ForceHTTPS && site.CertID != ""
}

// forceHTTPSRedirectTarget 构造 301 目标：仅把 scheme 换成 https，
// Host（含端口，同端口监听无需改写）、path 与 query 原样保留。
// Host 为空或含空白/控制字符时返回 ok=false，拒绝构造非法 Location。
func forceHTTPSRedirectTarget(r *http.Request) (string, bool) {
	host := r.Host
	if host == "" || strings.IndexFunc(host, func(c rune) bool {
		return c <= ' ' || c == 0x7f
	}) >= 0 {
		return "", false
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	target := "https://" + host + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target, true
}

// clientIP 只信任直接连接地址，避免攻击者伪造 X-Forwarded-For 绕过名单。
func clientIP(r *http.Request) string {
	return hostOnly(r.RemoteAddr)
}

// statusWriter 包装 ResponseWriter 以记录状态码与写出字节数，同时透传 Flush/Hijack，
// 保证 WebSocket 升级与流式响应不受影响。
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
	bytes  int64 // 累计写出的响应体字节数
	ss     *siteServer
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
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
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	// 登记 hijacked 连接（WebSocket 反代升级等），站点停止时可统一关闭；
	// 包装 Close 钩子，连接关闭时自动从登记表移除。
	if w.ss != nil {
		conn = &trackedConn{Conn: conn, ss: w.ss}
		w.ss.hijacked.Store(conn, struct{}{})
	}
	return conn, rw, nil
}

// writeNotFound 无匹配子规则时的 404 提示页。
func writeNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>404</title></head>
<body style="font-family:sans-serif;text-align:center;padding:60px 20px">
<h1>404</h1><p>没有匹配该请求的子规则</p><hr style="width:240px"><p>andey-proxy</p>
</body></html>`)
}
