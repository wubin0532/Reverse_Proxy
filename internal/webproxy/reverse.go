package webproxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"

	"luckyx/internal/config"
	"luckyx/internal/forward"
)

// reverseHandler 反向代理处理器：多后端简单轮询。
type reverseHandler struct {
	ruleName string
	proxies  []*httputil.ReverseProxy
	counter  atomic.Uint64
	logs     *forward.RingLog
}

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
			logs.Add(fmt.Sprintf("规则[%s] 后端地址无效: %q", rule.Name, b))
			continue
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		base := proxy.Director
		preserveHost := rule.PreserveHost
		headers := rule.Headers
		proxy.Director = func(req *http.Request) {
			base(req)
			if !preserveHost {
				// 不透传时把 Host 改为后端主机
				req.Host = target.Host
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
		// 立即刷新，保证 WebSocket/流式透传
		proxy.FlushInterval = -1
		ruleName := rule.Name
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			logs.Add(fmt.Sprintf("%s 规则[%s] 后端 %s 错误: %v", clientIP(r), ruleName, target.String(), err))
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		}
		h.proxies = append(h.proxies, proxy)
	}
	if len(h.proxies) == 0 {
		return nil, fmt.Errorf("reverse 规则无可用后端")
	}
	return h, nil
}

func (h *reverseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i := h.counter.Add(1) % uint64(len(h.proxies))
	h.proxies[i].ServeHTTP(w, r)
}
