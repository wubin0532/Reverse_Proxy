// Package webproxy 实现 Web 服务/反向代理核心模块：
// 站点监听管理、子规则分发（反代/跳转/文件服务）、安全组件与访问日志。
package webproxy

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"luckyx/internal/config"
	"luckyx/internal/forward"
)

// CertGetter 由外部（ACME 模块）注入的 SNI 证书回调。
// 返回错误或 nil 时会继续走证书文件与自签证书回退链。
type CertGetter func(hello *tls.ClientHelloInfo) (*tls.Certificate, error)

// Service 管理所有站点的监听器，支持按配置增量增删。
type Service struct {
	cfg        *config.Config
	certGetter CertGetter

	mu    sync.Mutex
	sites map[string]*siteServer

	certMu    sync.Mutex
	certFiles map[string]*certFileCache

	selfSignOnce sync.Once
	selfSignCert *tls.Certificate
	selfSignErr  error
}

// siteServer 一个站点监听器及其运行状态。
type siteServer struct {
	site config.Site // 启动时的配置快照，Reload 据此判断是否需要重启
	srv  *http.Server
	ln   net.Listener
	logs *forward.RingLog

	mu  sync.Mutex
	err error // 最近一次的监听/运行错误

	handlerMu  sync.Mutex
	revHandler map[string]http.Handler // reverse 规则处理器缓存（含轮询状态）
	revErr     map[string]error
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
	copy(sites, s.cfg.Sites)
	s.cfg.RUnlock()
	for _, site := range sites {
		if !site.Enabled {
			continue
		}
		if _, ok := s.sites[site.ID]; ok {
			continue
		}
		s.startLocked(site)
	}
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
}

// Reload 按当前配置增量调整监听器：
// 新增/重新启用 → 启动；删除/禁用/配置变化/处于错误状态 → 重启或停止。
func (s *Service) Reload() {
	s.cfg.RLock()
	want := make(map[string]config.Site)
	for _, site := range s.cfg.Sites {
		if site.Enabled {
			want[site.ID] = site
		}
	}
	s.cfg.RUnlock()

	var toStop []*siteServer
	var toStart []config.Site

	s.mu.Lock()
	for id, ss := range s.sites {
		ns, ok := want[id]
		switch {
		case !ok:
			// 已删除或禁用
			toStop = append(toStop, ss)
			delete(s.sites, id)
		case !reflect.DeepEqual(ss.site, ns) || ss.getErr() != nil:
			// 配置变化或上次启动失败，重启
			toStop = append(toStop, ss)
			delete(s.sites, id)
			toStart = append(toStart, ns)
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
	for _, site := range toStart {
		s.startLocked(site)
	}
	s.mu.Unlock()

	for _, ss := range toStop {
		stopSite(ss)
	}
}

// startLocked 启动单个站点监听器，失败时错误记录在 siteServer 上。
// 调用方须持有 s.mu。
func (s *Service) startLocked(site config.Site) {
	ss := &siteServer{
		site:       site,
		logs:       forward.NewRingLog(300),
		revHandler: make(map[string]http.Handler),
		revErr:     make(map[string]error),
	}
	s.sites[site.ID] = ss

	ln, err := net.Listen("tcp", site.Listen)
	if err != nil {
		ss.setErr(err)
		log.Printf("[webproxy] 站点 %s 监听 %s 失败: %v", site.Name, site.Listen, err)
		return
	}
	ss.ln = ln
	ss.srv = &http.Server{
		Handler:           &siteHandler{ss: ss},
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// 不设 WriteTimeout，避免掐断 WebSocket/流式长连接
	}
	log.Printf("[webproxy] 站点 %s 开始监听 %s (TLS=%v)", site.Name, site.Listen, site.TLS)
	go func() {
		var err error
		if site.TLS {
			err = ss.srv.Serve(tls.NewListener(ln, s.tlsConfig(site)))
		} else {
			err = ss.srv.Serve(ln)
		}
		if err != nil && err != http.ErrServerClosed {
			ss.setErr(err)
			log.Printf("[webproxy] 站点 %s 服务异常: %v", site.Name, err)
		}
	}()
}

func stopSite(ss *siteServer) {
	if ss.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ss.srv.Shutdown(ctx); err != nil {
		log.Printf("[webproxy] 站点 %s 关闭异常: %v", ss.site.Name, err)
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
func (s *Service) tlsConfig(site config.Site) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
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
	})
	return s.selfSignCert, s.selfSignErr
}
