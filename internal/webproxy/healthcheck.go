package webproxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// HealthCheckConf 子规则主动健康检查配置。Enabled 为 false（默认）时维持
// 纯被动熔断行为；启用后探测结果为准：被动失败可立即摘除，恢复必须探测成功。
//
// 配置不挂在 config.SubRule 上（该包由其他模块维护），由 healthStore
// 按规则 ID 持久化到配置目录下的 webproxy-health.json。
type HealthCheckConf struct {
	Enabled         bool   `json:"enabled"`
	Type            string `json:"type"`            // tcp / http，默认 tcp
	Path            string `json:"path"`            // http 探测路径，默认 /
	IntervalSeconds int    `json:"intervalSeconds"` // 探测间隔，默认 10
	TimeoutSeconds  int    `json:"timeoutSeconds"`  // 单次探测超时，默认 3
	Rise            int    `json:"rise"`            // 连续成功多少次恢复，默认 1
	Fall            int    `json:"fall"`            // 连续失败多少次摘除，默认 2
}

func (c HealthCheckConf) withDefaults() HealthCheckConf {
	if c.Type == "" {
		c.Type = "tcp"
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 10
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 3
	}
	if c.Rise == 0 {
		c.Rise = 1
	}
	if c.Fall == 0 {
		c.Fall = 2
	}
	return c
}

func (c *HealthCheckConf) validate() error {
	*c = c.withDefaults()
	if c.Type != "tcp" && c.Type != "http" {
		return fmt.Errorf("健康检查类型必须是 tcp 或 http")
	}
	if !strings.HasPrefix(c.Path, "/") || strings.ContainsAny(c.Path, " \r\n\t") {
		return fmt.Errorf("健康检查路径必须以 / 开头且不含空白字符")
	}
	if c.Type == "tcp" {
		c.Path = "/"
	}
	if c.IntervalSeconds < 2 || c.IntervalSeconds > 300 {
		return fmt.Errorf("健康检查间隔必须为 2 到 300 秒")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 60 {
		return fmt.Errorf("健康检查超时必须为 1 到 60 秒")
	}
	if c.TimeoutSeconds > c.IntervalSeconds {
		return fmt.Errorf("健康检查超时不能大于探测间隔")
	}
	if c.Rise < 1 || c.Rise > 10 {
		return fmt.Errorf("恢复阈值必须为 1 到 10 次")
	}
	if c.Fall < 1 || c.Fall > 10 {
		return fmt.Errorf("摘除阈值必须为 1 到 10 次")
	}
	return nil
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

// healthStore 按规则 ID 持久化健康检查配置（webproxy 自有边车文件，
// 避免改动 config 包的结构体）。读取结果常驻内存，磁盘仅在变更时写。
type healthStore struct {
	mu    sync.Mutex
	path  string
	confs map[string]HealthCheckConf // ruleID -> conf
}

func newHealthStore(dir string) *healthStore {
	st := &healthStore{path: filepath.Join(dir, "webproxy-health.json"), confs: make(map[string]HealthCheckConf)}
	data, err := os.ReadFile(st.path)
	if err != nil {
		return st
	}
	var file struct {
		Rules map[string]HealthCheckConf `json:"rules"`
	}
	if json.Unmarshal(data, &file) == nil {
		for id, conf := range file.Rules {
			st.confs[id] = conf.withDefaults()
		}
	}
	return st
}

func (st *healthStore) confFor(ruleID string) HealthCheckConf {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.confs[ruleID].withDefaults()
}

func (st *healthStore) set(ruleID string, conf HealthCheckConf) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.confs[ruleID] = conf.withDefaults()
	return st.saveLocked()
}

func (st *healthStore) delete(ruleID string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.confs[ruleID]; !ok {
		return nil
	}
	delete(st.confs, ruleID)
	return st.saveLocked()
}

// prune 清理已不存在规则的配置（规则/站点删除后调用）。keep 为全部现存规则 ID。
func (st *healthStore) prune(keep map[string]bool) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	changed := false
	for id := range st.confs {
		if !keep[id] {
			delete(st.confs, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return st.saveLocked()
}

func (st *healthStore) saveLocked() error {
	data, err := json.Marshal(struct {
		Rules map[string]HealthCheckConf `json:"rules"`
	}{Rules: st.confs})
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}
