package webproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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

// reverseHandler 反向代理处理器：多后端简单轮询。
type reverseHandler struct {
	ruleName string
	proxies  []proxyEntry
	counter  atomic.Uint64
	logs     *forward.RingLog
}

type proxyEntry struct {
	proxy     *httputil.ReverseProxy
	transport *http.Transport
}

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
		proxy := &httputil.ReverseProxy{}
		preserveHost := rule.PreserveHost
		autoHeaders := rule.ProxyHeadersEnabled()
		headers := rule.Headers
		frontendPrefix := normalizedFrontendPrefix(rule.FrontendPath)
		proxy.Rewrite = func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			if rule.StripPrefix {
				stripped := stripFrontendPrefix(pr.In.URL.Path, frontendPrefix)
				pr.Out.URL.Path, pr.Out.URL.RawPath = joinProxyURLPath(target, &url.URL{Path: stripped})
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
		proxy.Transport = transport
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
			message := fmt.Sprintf("%s 规则[%s] 后端 %s 错误: %s", clientIP(r), ruleName, backendURLForLog(target), proxyErrorForLog(err))
			logs.Add(message)
			logcenter.Add("webproxy", rule.ID, clientIP(r), "error", message)
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "413 Request Entity Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		}
		h.proxies = append(h.proxies, proxyEntry{proxy: proxy, transport: transport})
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

func (h *reverseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i := (h.counter.Add(1) - 1) % uint64(len(h.proxies))
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	ctx := context.WithValue(r.Context(), publicRequestKey{}, publicRequestInfo{scheme: scheme, host: r.Host})
	h.proxies[i].proxy.ServeHTTP(w, r.WithContext(ctx))
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

func stripFrontendPrefix(path, prefix string) string {
	prefix = normalizedFrontendPrefix(prefix)
	if prefix == "/" {
		return path
	}
	stripped := strings.TrimPrefix(path, prefix)
	if stripped == "" {
		return "/"
	}
	if !strings.HasPrefix(stripped, "/") {
		return "/" + stripped
	}
	return stripped
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
