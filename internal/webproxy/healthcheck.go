package webproxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/notify"
)

// 后端健康事件类型：经 notify 包级总线发布（与站点监听错误同一机制），
// 使用 site 前缀，订阅方按前缀匹配即可收到。
const (
	eventBackendDown = "site.backend_down"
	eventBackendUp   = "site.backend_up"
)

// HealthCheckConf preserves the health API shape; values live in encrypted Config.
type HealthCheckConf config.HealthCheckConf

func (c HealthCheckConf) withDefaults() HealthCheckConf {
	return HealthCheckConf(config.HealthCheckConf(c).WithDefaults())
}

func (c *HealthCheckConf) validate() error {
	conf := config.HealthCheckConf(*c)
	err := conf.Validate()
	*c = HealthCheckConf(conf)
	return err
}

// BackendHealth 单个后端的健康快照（API 响应）。
type BackendHealth struct {
	Backend     string `json:"backend"`
	Up          bool   `json:"up"`
	ActiveCheck bool   `json:"activeCheck"`
	LastError   string `json:"lastError,omitempty"`
	LastCheck   int64  `json:"lastCheck,omitempty"` // 最近探测时间（Unix 秒）
	LatencyMs   int64  `json:"latencyMs,omitempty"`
}

// configureHealth 应用（或关闭）主动健康检查；配置相同则不动。
// 每次构建/命中处理器缓存时由 reverseHandlerFor 调和一次，健康 API 变更时也直接调用。
func (h *reverseHandler) configureHealth(conf HealthCheckConf, rule config.SubRule) {
	conf = conf.withDefaults()
	h.healthMu.Lock()
	defer h.healthMu.Unlock()
	if h.healthConf == conf {
		return
	}
	h.stopProberLocked()
	h.healthConf = conf
	h.activeCheck.Store(conf.Enabled)
	if !conf.Enabled {
		// 回到纯被动模式：清除主动检查留下的摘除标记。
		for _, e := range h.proxies {
			e.up.Store(true)
		}
		return
	}
	stop := make(chan struct{})
	h.stopProbe = stop
	go h.probeLoop(conf, rule, stop)
}

func (h *reverseHandler) stopProberLocked() {
	if h.stopProbe != nil {
		close(h.stopProbe)
		h.stopProbe = nil
	}
}

// shutdown 停止探测并释放后端空闲连接（规则变更重建或站点停止时调用）。
func (h *reverseHandler) shutdown() {
	h.healthMu.Lock()
	h.stopProberLocked()
	h.healthMu.Unlock()
	h.closeIdleConnections()
}

type probeState struct {
	rise int
	fall int
}

// probeLoop 单规则探测协程：启动后立即探测一轮，之后按间隔探测全部后端。
func (h *reverseHandler) probeLoop(conf HealthCheckConf, rule config.SubRule, stop chan struct{}) {
	timeout := time.Duration(conf.TimeoutSeconds) * time.Second
	dialer := &net.Dialer{Timeout: timeout}
	var client *http.Client
	if conf.Type == "http" {
		client = &http.Client{Transport: proxyTransport(rule), Timeout: timeout}
		defer client.CloseIdleConnections()
	}
	states := make(map[*proxyEntry]*probeState, len(h.proxies))
	for _, e := range h.proxies {
		states[e] = &probeState{}
	}
	ticker := time.NewTicker(time.Duration(conf.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		h.probeRound(conf, dialer, client, states)
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func (h *reverseHandler) probeRound(conf HealthCheckConf, dialer *net.Dialer, client *http.Client, states map[*proxyEntry]*probeState) {
	for _, e := range h.proxies {
		latency, err := probeBackend(e, conf, dialer, client)
		st := states[e]
		e.lastCheckUnix.Store(time.Now().Unix())
		if err == nil {
			e.latencyNanos.Store(int64(latency))
			e.lastErr.Store("")
			st.rise++
			st.fall = 0
			if st.rise >= conf.Rise {
				h.markUp(e)
			}
			continue
		}
		e.lastErr.Store(err.Error())
		st.fall++
		st.rise = 0
		if st.fall >= conf.Fall {
			h.markDown(e, err.Error())
		}
	}
}

// probeBackend 探测单个后端：tcp 为纯拨号（含 TLS 后端也只测连通性），
// http 为 GET 探测路径，5xx 视为失败，其余状态码视为存活。
func probeBackend(e *proxyEntry, conf HealthCheckConf, dialer *net.Dialer, client *http.Client) (time.Duration, error) {
	start := time.Now()
	if conf.Type == "http" {
		probeURL := url.URL{Scheme: e.target.Scheme, Host: e.target.Host, Path: conf.Path}
		req, err := http.NewRequest(http.MethodGet, probeURL.String(), nil)
		if err != nil {
			return 0, err
		}
		res, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		_ = res.Body.Close()
		if res.StatusCode >= 500 {
			return time.Since(start), fmt.Errorf("状态码 %d", res.StatusCode)
		}
		return time.Since(start), nil
	}
	conn, err := dialer.Dial("tcp", backendDialAddress(e.target))
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

func backendDialAddress(target *url.URL) string {
	if target.Port() != "" {
		return target.Host
	}
	port := "80"
	if target.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(target.Hostname(), port)
}

// markUp 探测恢复：清零被动计数与冷却，状态翻转时上报事件。
func (h *reverseHandler) markUp(e *proxyEntry) {
	e.failures.Store(0)
	e.coolUntil.Store(0)
	if e.up.CompareAndSwap(false, true) {
		message := fmt.Sprintf("规则[%s] 后端 %s 恢复健康", h.ruleName, e.backend)
		h.logs.Add(message)
		logcenter.Add("webproxy", h.ruleID, "", "info", message)
		notify.Publish(notify.Event{Type: eventBackendUp, Entity: h.ruleName, Level: notify.LevelInfo, Message: message})
	}
}

// markDown 摘除后端：主动探测失败达阈值或（开启主动检查时）被动连续连接失败。
func (h *reverseHandler) markDown(e *proxyEntry, errMsg string) {
	e.lastErr.Store(errMsg)
	if e.up.CompareAndSwap(true, false) {
		message := fmt.Sprintf("规则[%s] 后端 %s 摘除: %s", h.ruleName, e.backend, errMsg)
		h.logs.Add(message)
		logcenter.Add("webproxy", h.ruleID, "", "warn", message)
		notify.Publish(notify.Event{Type: eventBackendDown, Entity: h.ruleName, Level: notify.LevelError, Message: message})
	}
}

// backendHealth 各后端健康快照。未开启主动检查时 Up 仅反映被动冷却状态，
// 无探测时间与延迟数据。
func (h *reverseHandler) backendHealth() []BackendHealth {
	now := time.Now().UnixNano()
	active := h.activeCheck.Load()
	out := make([]BackendHealth, 0, len(h.proxies))
	for _, e := range h.proxies {
		bh := BackendHealth{Backend: e.backend, ActiveCheck: active}
		if active {
			bh.Up = e.up.Load()
			bh.LastCheck = e.lastCheckUnix.Load()
			bh.LatencyMs = e.latencyNanos.Load() / int64(time.Millisecond)
		} else {
			bh.Up = e.coolUntil.Load() <= now
		}
		if v, ok := e.lastErr.Load().(string); ok && v != "" {
			bh.LastError = v
		}
		out = append(out, bh)
	}
	return out
}
