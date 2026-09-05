package webproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/forward"
	"andey-proxy/internal/logcenter"
)

// reverseHandler 反向代理处理器：多后端轮询，连接类失败达阈值的后端进入冷却期被跳过。
type reverseHandler struct {
	ruleName string
	proxies  []*proxyEntry
	counter  atomic.Uint64
	logs     *forward.RingLog
}

type proxyEntry struct {
	proxy     *httputil.ReverseProxy
	transport *http.Transport
	failures  atomic.Int32 // 连续连接类失败计数
	coolUntil atomic.Int64 // 冷却截止时间（UnixNano），0 = 未冷却
}

// 后端故障摘除：连续 2 次连接类失败后冷却 30 秒。
const (
	backendFailThreshold = 2
	backendCooldown      = 30 * time.Second
)

// noteFailure 记录一次连接类失败，达到阈值后进入冷却期。
func (e *proxyEntry) noteFailure() {
	if e.failures.Add(1) >= backendFailThreshold {
		e.coolUntil.Store(time.Now().Add(backendCooldown).UnixNano())
	}
}

// noteSuccess 成功请求清零失败计数。
func (e *proxyEntry) noteSuccess() {
	e.failures.Store(0)
}

// isBackendConnectError 判断是否为连接类错误（dial 失败、连接被拒绝、EOF 等
// 未拿到后端响应的错误）；后端返回的错误不计入失败计数。
func isBackendConnectError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// healthTransport 包装后端 Transport：连接类错误计入节点失败计数，成功清零。
type healthTransport struct {
	base  *http.Transport
	entry *proxyEntry
}

func (t *healthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err != nil {
		if isBackendConnectError(err) {
			t.entry.noteFailure()
		}
		return nil, err
	}
	t.entry.noteSuccess()
	return res, nil
}

// retriedKey 标记连接类错误已重试过一次，避免在多个后端间循环重试。
type retriedKey struct{}

// originalRequestKey retains the unmodified incoming request for failover.
type originalRequestKey struct{}

type publicRequestInfo struct {
	scheme string
	host   string
}

type publicRequestKey struct{}

// reverseHandlerFor 取（或惰性构建并缓存）某条 reverse 规则的处理器。
// 缓存挂在 siteServer 上，随站点重启失效。
func (ss *siteServer) reverseHandlerFor(rule *config.SubRule) (http.Handler, error) {
	ss.handlerMu.Lock()
	defer ss.handlerMu.Unlock()
	if !ss.currentRuleLocked(rule) {
		return nil, errRuleUpdated
	}
	if h, ok := ss.revHandler[rule.ID]; ok {
		return h, nil
	}
	if err, ok := ss.revErr[rule.ID]; ok {
		return nil, err
	}
	h, err := newReverseHandler(*rule, ss.logs)
	if err != nil {
		ss.revErr[rule.ID] = err
		return nil, err
	}
	ss.revHandler[rule.ID] = h
	return h, nil
}

// newReverseHandler 按规则构建反代处理器。
// 路径处理：保留完整请求路径（lucky 行为），后端 URL 自带的路径作为前缀拼接，
// 该行为由 httputil.NewSingleHostReverseProxy 的 joinURLPath 完成。
func newReverseHandler(rule config.SubRule, logs *forward.RingLog) (http.Handler, error) {
	if len(rule.Backends) == 0 {
		return nil, fmt.Errorf("reverse 规则未配置后端地址")
	}
	h := &reverseHandler{ruleName: rule.Name, logs: logs}
	for _, b := range rule.Backends {
		target, err := url.Parse(b)
		if err != nil || target.Scheme == "" || target.Host == "" {
			logs.Add(fmt.Sprintf("规则[%s] 后端地址无效", rule.Name))
			continue
		}
		entry := &proxyEntry{}
		proxy := &httputil.ReverseProxy{}
		preserveHost := rule.PreserveHost
		autoHeaders := rule.ProxyHeadersEnabled()
		headers := rule.Headers
		frontendPrefix := normalizedFrontendPrefix(rule.FrontendPath)
		proxy.Rewrite = func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			if rule.StripPrefix {
				stripped := stripFrontendURL(pr.In.URL, frontendPrefix)
				pr.Out.URL.Path, pr.Out.URL.RawPath = joinProxyURLPath(target, stripped)
			}
			if preserveHost {
				pr.Out.Host = pr.In.Host
			} else {
				pr.Out.Host = target.Host
			}
			for _, k := range []string{"Forwarded", "X-Forwarded-Host", "X-Real-IP", "X-Forwarded-For", "X-Forwarded-Proto", "X-Real-Proto", "X-Forwarded-Port", "X-Forwarded-Prefix"} {
				pr.Out.Header.Del(k)
			}
			if autoHeaders {
				pr.SetXForwarded()
				ip, _, err := net.SplitHostPort(pr.In.RemoteAddr)
				if err != nil {
					ip = pr.In.RemoteAddr
				}
				proto := "http"
				defaultPort := "80"
				if pr.In.TLS != nil {
					proto = "https"
					defaultPort = "443"
				}
				_, port, err := net.SplitHostPort(pr.In.Host)
				if err != nil {
					port = defaultPort
				}
				pr.Out.Header.Set("X-Real-IP", ip)
				pr.Out.Header.Set("X-Real-Proto", proto)
				pr.Out.Header.Set("X-Forwarded-Port", port)
			}
			if rule.StripPrefix && frontendPrefix != "/" {
				pr.Out.Header.Set("X-Forwarded-Prefix", frontendPrefix)
			}
			for k, v := range headers {
				if http.CanonicalHeaderKey(k) == "Host" {
					pr.Out.Host = v
				} else {
					pr.Out.Header.Set(k, v)
				}
			}
		}
		transport := proxyTransport(rule)
		entry.transport = transport
		proxy.Transport = &healthTransport{base: transport, entry: entry}
		// 普通响应批量刷新；ReverseProxy 会对 SSE 和未知长度流自动立即刷新。
		proxy.FlushInterval = 100 * time.Millisecond
		if rule.RewriteLocation || rule.CookieDomainFrom != "" || rule.CookiePathFrom != "" {
			proxy.ModifyResponse = func(res *http.Response) error {
				rewriteProxyResponse(res, target, rule)
				return nil
			}
		}
		ruleName := rule.Name
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			h.onProxyError(entry, target, rule.ID, ruleName, w, r, err)
		}
		entry.proxy = proxy
		h.proxies = append(h.proxies, entry)
	}
	if len(h.proxies) == 0 {
		return nil, fmt.Errorf("reverse 规则无可用后端")
	}
	return h, nil
}

func backendURLForLog(target *url.URL) string {
	clean := *target
	clean.User = nil
	clean.RawQuery = ""
	clean.Fragment = ""
	return clean.String()
}

func proxyErrorForLog(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Sprintf("%s: %v", urlErr.Op, urlErr.Err)
	}
	return err.Error()
}

// onProxyError 后端错误处理：连接类错误（未写出响应）时换一个可用后端重试一次，
// 其余情况维持 413/502 响应。失败计数由 healthTransport 在 RoundTrip 时记录。
func (h *reverseHandler) onProxyError(entry *proxyEntry, target *url.URL, ruleID, ruleName string, w http.ResponseWriter, r *http.Request, err error) {
	message := fmt.Sprintf("%s 规则[%s] 后端 %s 错误: %s", clientIP(r), ruleName, backendURLForLog(target), proxyErrorForLog(err))
	h.logs.Add(message)
	logcenter.Add("webproxy", ruleID, clientIP(r), "error", message)
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		http.Error(w, "413 Request Entity Too Large", http.StatusRequestEntityTooLarge)
		return
	}
	// Only safe methods without a body are automatically replayed. EOF can mean
	// the backend already executed the request, not just a failed dial.
	original, _ := r.Context().Value(originalRequestKey{}).(*http.Request)
	replayable := original != nil && (original.Method == http.MethodGet || original.Method == http.MethodHead || original.Method == http.MethodOptions) && original.ContentLength == 0 && (original.Body == nil || original.Body == http.NoBody)
	if isBackendConnectError(err) && replayable && r.Context().Err() == nil && r.Context().Value(retriedKey{}) == nil && len(h.proxies) > 1 {
		if alt := h.pick(entry); alt != entry {
			h.logs.Add(fmt.Sprintf("规则[%s] 后端连接失败，重试其他后端", ruleName))
			ctx := context.WithValue(r.Context(), retriedKey{}, true)
			alt.proxy.ServeHTTP(w, original.Clone(ctx))
			return
		}
	}
	http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
}

// pick 选一个未进入冷却期的后端（从轮询位置起向后找）；exclude 为重试时
// 排除刚失败的节点。全部在冷却期时回退为轮询全部节点。
func (h *reverseHandler) pick(exclude *proxyEntry) *proxyEntry {
	n := len(h.proxies)
	start := int((h.counter.Add(1) - 1) % uint64(n))
	now := time.Now().UnixNano()
	for k := 0; k < n; k++ {
		e := h.proxies[(start+k)%n]
		if e != exclude && e.coolUntil.Load() <= now {
			return e
		}
	}
	for k := 0; k < n; k++ {
		e := h.proxies[(start+k)%n]
		if e != exclude {
			return e
		}
	}
	return exclude
}

func (h *reverseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	entry := h.pick(nil)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	ctx := context.WithValue(r.Context(), originalRequestKey{}, r.Clone(r.Context()))
	ctx = context.WithValue(ctx, publicRequestKey{}, publicRequestInfo{scheme: scheme, host: r.Host})
	entry.proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (h *reverseHandler) closeIdleConnections() {
	for _, entry := range h.proxies {
		entry.transport.CloseIdleConnections()
	}
}

func proxyTransport(rule config.SubRule) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	transport.ForceAttemptHTTP2 = true
	if rule.ConnectTimeout > 0 {
		transport.DialContext = (&net.Dialer{Timeout: time.Duration(rule.ConnectTimeout) * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	if rule.ResponseHeaderTimeout > 0 {
		transport.ResponseHeaderTimeout = time.Duration(rule.ResponseHeaderTimeout) * time.Second
	}
	if rule.SkipBackendTLSVerify {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} // #nosec G402: explicit per-rule opt-in
	}
	return transport
}

func normalizedFrontendPrefix(prefix string) string {
	if prefix == "" {
		return "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		return "/" + prefix
	}
	return prefix
}

// stripFrontendURL trims the same decoded prefix from Path and EscapedPath,
// retaining encoded separators in the remainder (e.g. a%2Fb).
func stripFrontendURL(u *url.URL, prefix string) *url.URL {
	prefix = normalizedFrontendPrefix(prefix)
	out := *u
	if prefix == "/" || !strings.HasPrefix(u.Path, prefix) {
		return &out
	}
	raw := u.EscapedPath()
	offset := 0
	for n := 0; n < len(prefix); n++ {
		if raw[offset] == '%' {
			offset += 3
		} else {
			offset++
		}
	}
	out.Path = strings.TrimPrefix(u.Path, prefix)
	out.RawPath = raw[offset:]
	if strings.HasPrefix(out.Path, "/") && !strings.HasPrefix(out.RawPath, "/") {
		// The boundary slash is structural even when the client encoded it.
		out.RawPath = "/" + out.RawPath[3:]
	}
	if !strings.HasPrefix(out.Path, "/") {
		out.Path = "/" + out.Path
		out.RawPath = "/" + out.RawPath
	}
	return &out
}

func joinProxyURLPath(a, b *url.URL) (path, rawPath string) {
	if a.RawPath == "" && b.RawPath == "" {
		aSlash, bSlash := strings.HasSuffix(a.Path, "/"), strings.HasPrefix(b.Path, "/")
		switch {
		case aSlash && bSlash:
			return a.Path + b.Path[1:], ""
		case !aSlash && !bSlash:
			return a.Path + "/" + b.Path, ""
		default:
			return a.Path + b.Path, ""
		}
	}
	aPath, bPath := a.EscapedPath(), b.EscapedPath()
	aSlash, bSlash := strings.HasSuffix(aPath, "/"), strings.HasPrefix(bPath, "/")
	switch {
	case aSlash && bSlash:
		return a.Path + b.Path[1:], aPath + bPath[1:]
	case !aSlash && !bSlash:
		return a.Path + "/" + b.Path, aPath + "/" + bPath
	default:
		return a.Path + b.Path, aPath + bPath
	}
}

func rewriteProxyResponse(res *http.Response, target *url.URL, rule config.SubRule) {
	if rule.RewriteLocation {
		if location, err := url.Parse(res.Header.Get("Location")); err == nil && location.IsAbs() && strings.EqualFold(location.Host, target.Host) {
			if info, ok := res.Request.Context().Value(publicRequestKey{}).(publicRequestInfo); ok {
				location.Scheme = info.scheme
				location.Host = info.host
				res.Header.Set("Location", location.String())
			}
		}
	}
	if rule.CookieDomainFrom == "" && rule.CookiePathFrom == "" {
		return
	}
	cookies := res.Cookies()
	if len(cookies) == 0 {
		return
	}
	res.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if rule.CookieDomainFrom != "" && strings.EqualFold(strings.TrimPrefix(cookie.Domain, "."), strings.TrimPrefix(rule.CookieDomainFrom, ".")) {
			cookie.Domain = rule.CookieDomainTo
		}
		if rule.CookiePathFrom != "" && cookie.Path == rule.CookiePathFrom {
			cookie.Path = rule.CookiePathTo
		}
		res.Header.Add("Set-Cookie", cookie.String())
	}
}
