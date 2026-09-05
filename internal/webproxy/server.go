// Package webproxy 实现 Web 服务/反向代理核心模块：
// 站点监听管理、子规则分发（反代/跳转/文件服务）、安全组件与访问日志。
package webproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"andey-proxy/internal/config"
	"andey-proxy/internal/firewall"
	"andey-proxy/internal/forward"
	"andey-proxy/internal/guard"
	"andey-proxy/internal/netutil"
	"andey-proxy/internal/notify"
)

// CertGetter 由外部（ACME 模块）注入的 SNI 证书回调。
// 返回错误或 nil 时会继续走证书文件与自签证书回退链。
type CertGetter func(hello *tls.ClientHelloInfo) (*tls.Certificate, error)

// Service 管理所有站点的监听器，支持按配置增量增删。
type Service struct {
	cfg        *config.Config
	certGetter CertGetter

	// FW 可选的防火墙自动放行管理器（main 注入，nil 时跳过）。
	// Start/Reload 后会按 Enabled && AutoFW 的站点上报期望放行集合。
	FW *firewall.Manager

	mu       sync.Mutex
	reloadMu sync.Mutex
	sites    map[string]*siteServer

	// stats 站点级流量统计（siteID → 桶），独立于站点监听器生命周期：
	// 热重载重建 handler 缓存不丢失，站点删除/禁用时清理，进程重启清零。
	stats sync.Map

	certMu    sync.Mutex
	certFiles map[string]*certFileCache

	selfSignOnce sync.Once
	selfSignCert *tls.Certificate
	selfSignErr  error
}

// siteServer 一个站点监听器及其运行状态。
type siteServer struct {
	siteMu  sync.RWMutex
	site    config.Site // 当前不可变配置快照
	srv     *http.Server
	ln      net.Listener
	logs    *forward.RingLog
	limiter *ruleLimiter

	mu  sync.Mutex
	err error // 最近一次的监听/运行错误

	handlerMu     sync.Mutex
	ruleIndex     map[string]*config.SubRule
	ipGuards      map[string]*guard.IPMatcher
	revHandler    map[string]http.Handler // reverse 规则处理器缓存（含轮询状态）
	revErr        map[string]error
	staticHandler map[string]http.Handler // redirect/fileserver 规则处理器缓存（无内部状态）

	// hijacked 登记被 Hijack 的连接（WebSocket 反代升级等）：
	// http.Server.Shutdown 不跟踪此类连接，站点停止时需统一关闭。
	hijacked sync.Map

	stats *siteStats // Service 级统计桶，监听器重建后仍保留（直接构造的测试实例可为 nil）
}

// certFileCache 证书文件缓存，按修改时间失效。
type certFileCache struct {
	cert    *tls.Certificate
	modTime time.Time
}

// NewService 创建 Web 服务。certGetter 可为 nil（完全使用文件/自签证书）。
func NewService(cfg *config.Config, certGetter CertGetter) *Service {
	return &Service{
		cfg:        cfg,
		certGetter: certGetter,
		sites:      make(map[string]*siteServer),
		certFiles:  make(map[string]*certFileCache),
	}
}

// Start 启动所有已启用站点的监听器。单个站点失败不影响其他站点。
func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.RLock()
	sites := make([]config.Site, len(s.cfg.Sites))
	for i := range s.cfg.Sites {
		sites[i] = cloneSite(s.cfg.Sites[i])
	}
	s.cfg.RUnlock()
	for _, site := range sites {
		if !site.Enabled {
			continue
		}
		if _, ok := s.sites[site.ID]; ok {
			continue
		}
		_ = s.startLocked(site)
	}
	s.syncFirewall()
}

// Stop 优雅关闭所有站点（每个最多等 5 秒）。
func (s *Service) Stop() {
	s.mu.Lock()
	all := make([]*siteServer, 0, len(s.sites))
	for id, ss := range s.sites {
		all = append(all, ss)
		delete(s.sites, id)
	}
	s.mu.Unlock()
	for _, ss := range all {
		stopSite(ss)
	}
	// 服务整体停止：清空全部站点统计
	s.stats.Range(func(key, _ any) bool {
		s.deleteStats(key.(string))
		return true
	})
	// 服务整体停止：清空 web 来源的自动放行规则
	if s.FW != nil {
		s.FW.SetDesiredFrom(firewall.SourceWeb, nil)
	}
}

// Reload 按当前配置增量调整监听器：
// 新增/重新启用 → 启动；删除/禁用/配置变化/处于错误状态 → 重启或停止。
func (s *Service) Reload() error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.cfg.RLock()
	want := make(map[string]config.Site)
	for _, site := range s.cfg.Sites {
		if site.Enabled {
			want[site.ID] = cloneSite(site)
		}
	}
	s.cfg.RUnlock()

	var toStop []*siteServer
	var toStart []config.Site
	var firstErr error

	s.mu.Lock()
	for id, ss := range s.sites {
		ns, ok := want[id]
		current := ss.siteSnapshot()
		switch {
		case !ok:
			// 已删除或禁用
			toStop = append(toStop, ss)
			delete(s.sites, id)
			s.deleteStats(id)
		case current.Listen != ns.Listen || current.TLS != ns.TLS ||
			forceHTTPSActive(current) != forceHTTPSActive(ns) || ss.getErr() != nil:
			// 只有监听地址、明文/TLS 模式（含强制 HTTPS 的嗅探分流）变化或运行错误才重建监听器。
			toStop = append(toStop, ss)
			delete(s.sites, id)
			toStart = append(toStart, ns)
		case !reflect.DeepEqual(current, ns):
			// 非监听配置热更新，并重建规则缓存。
			ss.updateSite(ns)
		}
	}
	for id, site := range want {
		if _, ok := s.sites[id]; !ok {
			// 避免与上面重启分支重复加入
			dup := false
			for _, x := range toStart {
				if x.ID == id {
					dup = true
					break
				}
			}
			if !dup {
				toStart = append(toStart, site)
			}
		}
	}
	s.mu.Unlock()

	for _, ss := range toStop {
		stopSite(ss)
	}
	s.mu.Lock()
	for _, site := range toStart {
		if err := s.startLocked(site); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.mu.Unlock()
	s.syncFirewall()
	return firstErr
}

// syncFirewall 按当前配置向防火墙管理器上报 web 来源的期望放行集合：
// Enabled && AutoFW 的站点，从 Listen（如 ":8080"）解析端口，协议恒为 tcp。
// 解析失败或非 OpenWrt 环境仅记日志，不影响主流程。
func (s *Service) syncFirewall() {
	if s.FW == nil {
		return
	}
	s.cfg.RLock()
	var rules []firewall.Rule
	for _, site := range s.cfg.Sites {
		if !site.Enabled || !site.AutoFW {
			continue
		}
		port, err := netutil.ListenPort(site.Listen)
		if err != nil {
			log.Printf("[webproxy] 站点 %s 监听地址 %q 端口解析失败，跳过自动放行: %v", site.Name, site.Listen, err)
			continue
		}
		rules = append(rules, firewall.Rule{Key: site.ID, Port: port, Proto: "tcp"})
	}
	s.cfg.RUnlock()
	s.FW.SetDesiredFrom(firewall.SourceWeb, rules)
}

// startLocked 启动单个站点监听器，失败时错误记录在 siteServer 上。
// 调用方须持有 s.mu。
func (s *Service) startLocked(site config.Site) error {
	site = cloneSite(site)
	ss := &siteServer{
		site:          site,
		logs:          forward.NewRingLog(300),
		limiter:       newRuleLimiter(),
		revHandler:    make(map[string]http.Handler),
		revErr:        make(map[string]error),
		staticHandler: make(map[string]http.Handler),
		stats:         s.statsFor(site.ID),
	}
	s.sites[site.ID] = ss

	ln, err := net.Listen("tcp", site.Listen)
	if err != nil {
		ss.setErr(err)
		log.Printf("[webproxy] 站点 %s 监听 %s 失败: %v", site.Name, site.Listen, err)
		notify.Publish(notify.Event{Type: notify.TypeSiteListenError, Entity: site.Name, Level: notify.LevelError, Message: "站点 " + site.Name + " 监听 " + site.Listen + " 失败: " + err.Error()})
		return err
	}
	ss.ln = ln
	ss.srv = &http.Server{
		Handler:           &siteHandler{ss: ss},
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// 不设 WriteTimeout，避免掐断 WebSocket/流式长连接
	}
	if site.TLS {
		ss.srv.TLSConfig = s.tlsConfig(ss.siteSnapshot)
		if err := http2.ConfigureServer(ss.srv, &http2.Server{}); err != nil {
			_ = ln.Close()
			ss.setErr(err)
			return err
		}
	}
	log.Printf("[webproxy] 站点 %s 开始监听 %s (TLS=%v, forceHTTPS=%v)", site.Name, site.Listen, site.TLS, forceHTTPSActive(site))
	go func() {
		var err error
		switch {
		case site.TLS && forceHTTPSActive(site):
			// 强制 HTTPS：同端口按首字节嗅探分流 TLS 与明文
			err = ss.srv.Serve(&tlsSniffListener{Listener: ln, tlsConfig: ss.srv.TLSConfig})
		case site.TLS:
			err = ss.srv.Serve(tls.NewListener(ln, ss.srv.TLSConfig))
		default:
			err = ss.srv.Serve(ln)
		}
		if err != nil && err != http.ErrServerClosed {
			ss.setErr(err)
			log.Printf("[webproxy] 站点 %s 服务异常: %v", site.Name, err)
		}
	}()
	return nil
}

func stopSite(ss *siteServer) {
	ss.closeIdleConnections()
	if ss.srv == nil {
		ss.closeHijackedConnections()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ss.srv.Shutdown(ctx); err != nil {
		log.Printf("[webproxy] 站点 %s 关闭异常: %v", ss.siteSnapshot().Name, err)
	}
	// Shutdown 不跟踪被 Hijack 的连接（WebSocket 反代等），统一关闭登记在册的连接。
	// 仅覆盖经由 statusWriter.Hijack 升级的连接；连接关闭后反代侧 copy goroutine 随之退出。
	ss.closeHijackedConnections()
}

// closeHijackedConnections 关闭所有登记的 hijacked 连接（Close 会顺带从登记表移除）。
func (ss *siteServer) closeHijackedConnections() {
	ss.hijacked.Range(func(key, _ any) bool {
		if conn, ok := key.(*trackedConn); ok {
			_ = conn.Close()
		}
		return true
	})
}

// trackedConn 包装被 Hijack 的连接：登记到站点表，关闭时自动移除。
type trackedConn struct {
	net.Conn
	ss *siteServer
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.ss.hijacked.Delete(c)
	return err
}

func (ss *siteServer) siteSnapshot() config.Site {
	ss.siteMu.RLock()
	defer ss.siteMu.RUnlock()
	return ss.site
}

// addStats 记录一次请求的站点统计；测试直接构造的 siteServer 无统计桶时跳过。
func (ss *siteServer) addStats(status int, bytesIn, bytesOut int64) {
	if ss.stats != nil {
		ss.stats.add(status, bytesIn, bytesOut)
	}
}

var errRuleUpdated = errors.New("rule changed during request dispatch")

// Caller holds handlerMu. Live requests normally use the exact current pointer;
// old snapshots can reuse a handler only when the rule is unchanged.
func (ss *siteServer) currentRuleLocked(rule *config.SubRule) bool {
	if ss.ruleIndex == nil {
		site := ss.siteSnapshot()
		ss.ruleIndex = make(map[string]*config.SubRule, len(site.Rules))
		for i := range site.Rules {
			ss.ruleIndex[site.Rules[i].ID] = &site.Rules[i]
		}
	}
	current := ss.ruleIndex[rule.ID]
	return current != nil && (current == rule || reflect.DeepEqual(*current, *rule))
}

func (ss *siteServer) updateSite(site config.Site) {
	site = cloneSite(site)
	ss.handlerMu.Lock()
	previous := ss.siteSnapshot()
	oldRules := make(map[string]config.SubRule, len(previous.Rules))
	for _, rule := range previous.Rules {
		oldRules[rule.ID] = rule
	}
	nextRules := make(map[string]*config.SubRule, len(site.Rules))
	for i := range site.Rules {
		nextRules[site.Rules[i].ID] = &site.Rules[i]
	}
	var retired []*reverseHandler
	for id, oldRule := range oldRules {
		next := nextRules[id]
		if next != nil && reflect.DeepEqual(oldRule, *next) {
			continue
		}
		if h, ok := ss.revHandler[id].(*reverseHandler); ok {
			retired = append(retired, h)
		}
		delete(ss.revHandler, id)
		delete(ss.revErr, id)
		delete(ss.staticHandler, id)
		delete(ss.ipGuards, id)
	}
	// Publish the snapshot and its cache index together under handlerMu. A request
	// holding an older, changed rule is rejected instead of repopulating the cache.
	ss.siteMu.Lock()
	ss.site = site
	ss.siteMu.Unlock()
	ss.ruleIndex = nextRules
	ss.handlerMu.Unlock()
	for _, h := range retired {
		h.closeIdleConnections()
	}
}

// cloneSite creates an immutable runtime snapshot. A shallow struct copy would
// share rule slices and maps with Config, making changes invisible to Reload's
// comparison and racing with live request dispatch.
func cloneSite(site config.Site) config.Site {
	cloned := site
	cloned.Rules = make([]config.SubRule, len(site.Rules))
	for i := range site.Rules {
		cloned.Rules[i] = site.Rules[i]
		cloned.Rules[i].Backends = append([]string(nil), site.Rules[i].Backends...)
		cloned.Rules[i].IPList = append([]string(nil), site.Rules[i].IPList...)
		cloned.Rules[i].UAList = append([]string(nil), site.Rules[i].UAList...)
		if site.Rules[i].Headers != nil {
			cloned.Rules[i].Headers = make(map[string]string, len(site.Rules[i].Headers))
			for key, value := range site.Rules[i].Headers {
				cloned.Rules[i].Headers[key] = value
			}
		}
	}
	return cloned
}

func (ss *siteServer) closeIdleConnections() {
	ss.handlerMu.Lock()
	handlers := make([]http.Handler, 0, len(ss.revHandler))
	for _, handler := range ss.revHandler {
		handlers = append(handlers, handler)
	}
	ss.handlerMu.Unlock()
	for _, handler := range handlers {
		if reverse, ok := handler.(*reverseHandler); ok {
			reverse.closeIdleConnections()
		}
	}
}

func (ss *siteServer) setErr(err error) {
	ss.mu.Lock()
	ss.err = err
	ss.mu.Unlock()
}

func (ss *siteServer) getErr() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.err
}

// Logs 返回站点访问日志（最新在最后）。站点不存在或未运行时返回空。
func (s *Service) Logs(siteID string) []string {
	s.mu.Lock()
	ss := s.sites[siteID]
	s.mu.Unlock()
	if ss == nil {
		return []string{}
	}
	return ss.logs.Entries()
}

// SiteStatus 返回站点运行状态：listening / error / stopped。
func (s *Service) SiteStatus(siteID string) (status string, errMsg string) {
	s.mu.Lock()
	ss := s.sites[siteID]
	s.mu.Unlock()
	if ss == nil {
		return "stopped", ""
	}
	if err := ss.getErr(); err != nil {
		return "error", err.Error()
	}
	return "listening", ""
}

// ListenAddr 返回站点实际监听地址（"127.0.0.1:0" 场景下为分配到的端口），主要用于测试。
func (s *Service) ListenAddr(siteID string) string {
	s.mu.Lock()
	ss := s.sites[siteID]
	s.mu.Unlock()
	if ss == nil || ss.ln == nil {
		return ""
	}
	return ss.ln.Addr().String()
}

// tlsConfig 构建站点 TLS 配置，GetCertificate 回退链：
// 1) 注入的 CertGetter（ACME 模块） 2) 配置目录证书文件 3) 内存自签证书。
func (s *Service) tlsConfig(siteSnapshot func() config.Site) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			site := siteSnapshot()
			if s.certGetter != nil {
				if cert, err := s.certGetter(hello); err == nil && cert != nil {
					return cert, nil
				}
			}
			if site.CertID != "" {
				if cert, err := s.loadCertFile(site.CertID); err == nil {
					return cert, nil
				} else {
					log.Printf("[webproxy] 站点 %s 加载证书 %s 失败，使用自签证书: %v", site.Name, site.CertID, err)
				}
			}
			return s.selfSigned()
		},
	}
}

// AdminTLSConfig 为管理后台提供与 Web 站点相同的 SNI/自签回退链。
func (s *Service) AdminTLSConfig() *tls.Config {
	return s.tlsConfig(func() config.Site { return config.Site{Name: "管理后台"} })
}

// loadCertFile 从 cfg.Dir()/certs/<CertID>.crt/.key 加载证书，按文件修改时间缓存。
func (s *Service) loadCertFile(certID string) (*tls.Certificate, error) {
	base := filepath.Base(certID) // 防目录穿越
	dir := filepath.Join(s.cfg.Dir(), "certs")
	crtPath := filepath.Join(dir, base+".crt")
	keyPath := filepath.Join(dir, base+".key")

	var modTime time.Time
	for _, p := range []string{crtPath, keyPath} {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.ModTime().After(modTime) {
			modTime = fi.ModTime()
		}
	}

	s.certMu.Lock()
	defer s.certMu.Unlock()
	if c := s.certFiles[base]; c != nil && c.modTime.Equal(modTime) {
		return c.cert, nil
	}
	cert, err := tls.LoadX509KeyPair(crtPath, keyPath)
	if err != nil {
		return nil, err
	}
	s.certFiles[base] = &certFileCache{cert: &cert, modTime: modTime}
	return &cert, nil
}

// selfSigned 返回进程内唯一的自签证书（懒生成一次）。
func (s *Service) selfSigned() (*tls.Certificate, error) {
	s.selfSignOnce.Do(func() {
		s.selfSignCert, s.selfSignErr = generateSelfSigned()
		if s.selfSignErr == nil {
			log.Printf("管理后台正在使用自签 TLS 证书，SHA-256 指纹: %s，请核对后信任", certFingerprint(s.selfSignCert))
		}
	})
	return s.selfSignCert, s.selfSignErr
}
