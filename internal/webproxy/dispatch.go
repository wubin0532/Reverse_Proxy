package webproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

// middleware 可组合的请求处理中间件。
type middleware func(http.Handler) http.Handler

// compose 按声明顺序组装中间件链：mws[0] 在最外层（最先执行）。
func compose(base http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		base = mws[i](base)
	}
	return base
}

// matchedRuleKey 命中的子规则（ruleMatchMiddleware 注入请求上下文）。
type matchedRuleKey struct{}

// matchedRule 取上下文中的命中规则；链上 ruleMatchMiddleware 之后必有值。
func matchedRule(r *http.Request) *config.SubRule {
	return r.Context().Value(matchedRuleKey{}).(*config.SubRule)
}

// siteHandler 站点入口：访问日志与流量统计外壳，请求处理交给中间件链。
type siteHandler struct {
	ss *siteServer
}

func (h *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK, ss: h.ss}
	h.ss.beginSiteStats()
	defer func() {
		ruleID := h.ss.siteSnapshot().ID
		if sw.matchedRuleID != "" {
			ruleID = sw.matchedRuleID
		}
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
		h.ss.finishStats(sw.matchedRuleID, sw.status, bytesIn, sw.bytes)
	}()

	h.ss.dispatchChain().ServeHTTP(sw, r)
}

// dispatchChain 站点请求处理链（惰性构建一次；中间件均读取实时快照，无需随配置重建）。
// 顺序：路径净化 → 规则匹配 → 限流 → 强制 HTTPS → 安全组件 → 按类型分发。
func (ss *siteServer) dispatchChain() http.Handler {
	ss.chainOnce.Do(func() {
		ss.chain = compose(&ruleDispatch{ss: ss},
			pathSanityMiddleware(),
			ss.ruleMatchMiddleware(),
			ss.rateLimitMiddleware(),
			ss.forceHTTPSMiddleware(),
			ss.guardMiddleware(),
		)
	})
	return ss.chain
}

// pathSanityMiddleware 拒绝会被后端归一化成其他路径的请求（防绕过下游检查）。
func pathSanityMiddleware() middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ambiguousPath(r.URL.Path) {
				http.Error(w, "400 Bad Request", http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ruleMatchMiddleware 匹配子规则并注入上下文；命中后记入 statusWriter 并开启规则统计，
// 无匹配时写 404。
func (ss *siteServer) ruleMatchMiddleware() middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule := matchRule(ss.siteSnapshot().Rules, r.Host, r.URL.Path)
			if rule == nil {
				writeNotFound(w, r)
				return
			}
			if sw, ok := w.(*statusWriter); ok {
				sw.matchedRuleID = rule.ID
			}
			ss.beginRuleStats(rule.ID)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), matchedRuleKey{}, rule)))
		})
	}
}

// rateLimitMiddleware 按规则限流，超限时 429 并带 Retry-After。
func (ss *siteServer) rateLimitMiddleware() middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowed, retryAfter := ss.limiter.allow(matchedRule(r), clientIP(r), time.Now()); !allowed {
				w.Header().Set("Retry-After", fmt.Sprint(retryAfter))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// forceHTTPSMiddleware 强制 HTTPS：监听层嗅探分流出的明文连接（r.TLS == nil）301 跳转到 https，
// 同端口的 TLS 请求继续正常分发，避免循环跳转。
func (ss *siteServer) forceHTTPSMiddleware() middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil && forceHTTPSActive(ss.siteSnapshot()) {
				target, ok := forceHTTPSRedirectTarget(r)
				if !ok {
					http.Error(w, "400 Bad Request", http.StatusBadRequest)
					return
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// guardMiddleware 子规则安全组件（IP/UA 名单、BasicAuth）。
func (ss *siteServer) guardMiddleware() middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule := matchedRule(r)
			if !checkRuleGuard(w, r, rule, ss.logs, ss.ipGuardFor(rule)) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ruleDispatch 链末端：按规则类型分发到反代/跳转/文件服务。
type ruleDispatch struct {
	ss *siteServer
}

func (d *ruleDispatch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rule := matchedRule(r)
	switch rule.Type {
	case "reverse":
		if rule.MaxRequestBodyMiB > 0 {
			limit := int64(rule.MaxRequestBodyMiB) << 20
			if r.ContentLength > limit {
				http.Error(w, "413 Request Entity Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
		}
		rh, err := d.ss.reverseHandlerFor(rule)
		if errors.Is(err, errRuleUpdated) {
			http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			d.ss.logs.Add(fmt.Sprintf("%s 规则[%s] 反代不可用: %v", clientIP(r), rule.Name, err))
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
			return
		}
		rh.ServeHTTP(w, r)
	case "redirect", "fileserver":
		d.ss.staticHandlerFor(rule).ServeHTTP(w, r)
	default:
		writeNotFound(w, r)
	}
}

// staticHandlerFor 取（或惰性构建并缓存）redirect/fileserver 规则的处理器，
// 避免每请求重建。只有变更的规则在 updateSite 时失效；
// 两类处理器均无内部状态，无需额外清理。
func (ss *siteServer) staticHandlerFor(rule *config.SubRule) http.Handler {
	ss.handlerMu.Lock()
	defer ss.handlerMu.Unlock()
	if !ss.currentRuleLocked(rule) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
		})
	}
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

// Reject paths that backends commonly normalize into a different route. Check
// decoded Path as well, so percent-encoded dot segments cannot bypass guards.
func ambiguousPath(path string) bool {
	if strings.Contains(path, "\\") || strings.Contains(path, "//") {
		return true
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
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
	status        int
	wrote         bool
	bytes         int64  // 累计写出的响应体字节数
	matchedRuleID string // 命中的子规则 ID（ruleMatchMiddleware 记录，供统计/日志使用）
	ss            *siteServer
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
