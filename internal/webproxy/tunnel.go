package webproxy

import (
	"andey-proxy/internal/config"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type tunnelIdentityKey struct{}
type tunnelIdentity struct{ IP, Scheme string }
type tunnelOrigin struct {
	server      *http.Server
	path        string
	mu          sync.Mutex
	closed      bool
	connections map[*originConn]bool
}

type originConn struct {
	net.Conn
	origin *tunnelOrigin
}

func (c *originConn) Close() error {
	err := c.Conn.Close()
	c.origin.mu.Lock()
	delete(c.origin.connections, c)
	c.origin.mu.Unlock()
	return err
}

type originListener struct {
	net.Listener
	origin *tunnelOrigin
}

func (l *originListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.origin.mu.Lock()
	defer l.origin.mu.Unlock()
	if l.origin.closed {
		_ = c.Close()
		return nil, net.ErrClosed
	}
	tracked := &originConn{Conn: c, origin: l.origin}
	l.origin.connections[tracked] = true
	return tracked, nil
}
func (o *tunnelOrigin) close() {
	o.mu.Lock()
	o.closed = true
	connections := make([]*originConn, 0, len(o.connections))
	for c := range o.connections {
		connections = append(connections, c)
	}
	o.mu.Unlock()
	_ = o.server.Close()
	for _, c := range connections {
		_ = c.Close()
	}
}

// TunnelSocketPath is deterministic but never contains user-supplied path segments.
func TunnelSocketPath(dir, siteID string) string {
	return config.TunnelSocketPath(dir, siteID)
}

func requestScheme(r *http.Request) string {
	if identity, ok := r.Context().Value(tunnelIdentityKey{}).(tunnelIdentity); ok {
		return identity.Scheme
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (s *Service) tunnelHandler(siteID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(hostOnly(r.Host))
		s.cfg.RLock()
		allowed := s.cfg.TunnelSiteBindings(true)[siteID][host]
		s.cfg.RUnlock()
		if !allowed {
			http.NotFound(w, r)
			return
		}
		ip := net.ParseIP(r.Header.Get("CF-Connecting-IP"))
		scheme := r.Header.Get("X-Forwarded-Proto")
		if ip == nil || (scheme != "http" && scheme != "https") {
			http.Error(w, "Invalid tunnel identity", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		ss := s.sites[siteID]
		s.mu.Unlock()
		if ss == nil || !ss.siteSnapshot().Enabled || ss.getErr() != nil {
			http.Error(w, "Site unavailable", http.StatusServiceUnavailable)
			return
		}
		identity := tunnelIdentity{IP: ip.String(), Scheme: scheme}
		(&siteHandler{ss: ss}).ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tunnelIdentityKey{}, identity)))
	})
}

// SyncTunnelOrigins reconciles local listeners only; it never contacts Cloudflare.
func (s *Service) SyncTunnelOrigins() error {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.tunnelOrigins == nil {
		s.tunnelOrigins = make(map[string]*tunnelOrigin)
	}
	s.tunnelErrors = make(map[string]string)
	want := make(map[string]bool)
	s.cfg.RLock()
	for siteID := range s.cfg.TunnelSiteBindings(true) {
		for _, site := range s.cfg.Sites {
			if site.ID == siteID && site.Enabled {
				want[siteID] = true
			}
		}
	}
	s.cfg.RUnlock()
	for id, origin := range s.tunnelOrigins {
		if !want[id] {
			origin.close()
			_ = os.Remove(origin.path)
			delete(s.tunnelOrigins, id)
		}
	}
	var firstErr error
	for id := range want {
		if s.tunnelOrigins[id] != nil {
			continue
		}
		origin, err := s.createTunnelOrigin(id)
		if err != nil {
			s.tunnelErrors[id] = "隧道本机入口创建失败: " + err.Error()
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.tunnelOrigins[id] = origin
	}
	return firstErr
}

func (s *Service) createTunnelOrigin(id string) (*tunnelOrigin, error) {
	path := TunnelSocketPath(s.cfg.Dir(), id)
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("不安全的隧道运行目录")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("隧道 Socket 路径已被占用")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	server := &http.Server{Handler: s.tunnelHandler(id), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	origin := &tunnelOrigin{server: server, path: path, connections: make(map[*originConn]bool)}
	go func() {
		if err := server.Serve(&originListener{Listener: ln, origin: origin}); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.tunnelMu.Lock()
			defer s.tunnelMu.Unlock()
			if s.tunnelOrigins[id] == origin {
				delete(s.tunnelOrigins, id)
				s.tunnelErrors[id] = "隧道本机入口监听失败"
			}
		}
	}()
	return origin, nil
}

// TunnelOriginStatus reports the private listener independently of the TCP site.
func (s *Service) TunnelOriginStatus(siteID string) (string, string) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if message := s.tunnelErrors[siteID]; message != "" {
		return "error", message
	}
	if origin := s.tunnelOrigins[siteID]; origin != nil {
		info, err := os.Lstat(origin.path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return "error", "隧道 Socket 不可用"
		}
		return "listening", ""
	}
	return "stopped", ""
}

func (s *Service) stopTunnelOrigins() {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	for id, origin := range s.tunnelOrigins {
		origin.close()
		_ = os.Remove(origin.path)
		delete(s.tunnelOrigins, id)
	}
	s.tunnelErrors = nil
}
