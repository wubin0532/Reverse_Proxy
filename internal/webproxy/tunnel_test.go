package webproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func tunnelTestService(t *testing.T, backend string, rule config.SubRule) (*Service, *config.Config, *http.Client, string) {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfg.State().Close() })
	rule.ID = "rule"
	rule.Type = "reverse"
	rule.Enabled = true
	rule.FrontendHost = "app.example.com"
	rule.Backends = []string{backend}
	cfg.Update(func(c *config.Config) error {
		c.Sites = []config.Site{{ID: "site", Name: "test", Enabled: true, Listen: "127.0.0.1:0", Rules: []config.SubRule{rule}}}
		c.Tunnels = []config.TunnelInstance{{ID: "tunnel", Enabled: true, Routes: []config.TunnelRoute{{ID: "route", Hostname: "app.example.com", Target: "site", SiteID: "site"}}}}
		return nil
	})
	service := NewService(cfg, nil)
	service.Start()
	t.Cleanup(service.Stop)
	if err = service.SyncTunnelOrigins(); err != nil {
		t.Fatal(err)
	}
	path := TunnelSocketPath(cfg.Dir(), "site")
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(client.CloseIdleConnections)
	t.Cleanup(func() { service.Stop(); os.Remove(filepath.Dir(path)) })
	return service, cfg, client, path
}
func tunnelRequest(t *testing.T, client *http.Client, ip string, auth bool, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://app.example.com"+path, nil)
	req.Header.Set("CF-Connecting-IP", ip)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "attacker")
	if auth {
		req.SetBasicAuth("user", "pass")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func TestTunnelIdentityGuardHeadersRateLimitsAndSocketPermissions(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s|%s", r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("CF-Connecting-IP"))
	}))
	defer backend.Close()
	service, cfg, client, path := tunnelTestService(t, backend.URL, config.SubRule{BasicAuth: true, AuthUser: "user", AuthPass: "pass", IPListMode: "whitelist", IPList: []string{"203.0.113.11"}})
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0o700 {
		t.Fatal("socket directory permissions")
	}
	res := tunnelRequest(t, client, "203.0.113.11", true, "/")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "203.0.113.11|203.0.113.11|https|" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	res = tunnelRequest(t, client, "203.0.113.11", false, "/")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("BasicAuth bypassed")
	}
	res = tunnelRequest(t, client, "203.0.113.12", true, "/")
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("visitor IP guard bypassed")
	}
	req, _ := http.NewRequest("GET", "http://"+service.ListenAddr("site")+"/", nil)
	req.Host = "app.example.com"
	req.Header.Set("CF-Connecting-IP", "203.0.113.11")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.SetBasicAuth("user", "pass")
	plain := &http.Client{Timeout: time.Second}
	defer plain.CloseIdleConnections()
	res, err = plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("ordinary TCP trusted forged CF identity")
	}
	cfg.Update(func(c *config.Config) error {
		c.Sites[0].Rules[0].RateLimitRPS = 1
		c.Sites[0].Rules[0].RateLimitBurst = 1
		return nil
	})
	service.Reload()
	res = tunnelRequest(t, client, "203.0.113.11", true, "/")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("first request limited")
	}
	res = tunnelRequest(t, client, "203.0.113.11", true, "/")
	res.Body.Close()
	if res.StatusCode != 429 {
		t.Fatal("tunnel rate limit not applied")
	}
	logs := strings.Join(service.Logs("site"), "\n")
	if !strings.Contains(logs, "203.0.113.11") {
		t.Fatal("logs did not use visitor IP")
	}
}
func TestTunnelForceHTTPSAndLocationRewrite(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://"+r.Host+"/login")
		w.WriteHeader(302)
	}))
	defer backend.Close()
	service, cfg, client, _ := tunnelTestService(t, backend.URL, config.SubRule{RewriteLocation: true})
	cfg.Update(func(c *config.Config) error {
		c.Sites[0].TLS = true
		c.Sites[0].ForceHTTPS = true
		c.Sites[0].CertID = "cert"
		return nil
	})
	if err := service.Reload(); err != nil {
		t.Fatal(err)
	}
	res := tunnelRequest(t, client, "203.0.113.11", false, "/")
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "https://app.example.com/login" {
		t.Fatalf("redirect loop or wrong scheme: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}
func TestTunnelWebSocketAndSSE(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(buf, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		buf.Flush()
		io.Copy(conn, conn)
	}))
	defer backend.Close()
	_, _, client, path := tunnelTestService(t, backend.URL, config.SubRule{})
	res := tunnelRequest(t, client, "203.0.113.11", false, "/events")
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	res.Body.Close()
	if err != nil || line != "data: ready\n" {
		t.Fatalf("SSE not streamed: %q %v", line, err)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: app.example.com\r\nCF-Connecting-IP: 203.0.113.11\r\nX-Forwarded-Proto: https\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != 101 {
		t.Fatalf("upgrade failed: %v %v", response, err)
	}
	fmt.Fprint(conn, "hello")
	echo := make([]byte, 5)
	if _, err = io.ReadFull(reader, echo); err != nil || string(echo) != "hello" {
		t.Fatalf("upgrade stream failed: %q %v", echo, err)
	}
}
func TestTunnelHostRestrictionAndSiteLifecycle(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer backend.Close()
	service, cfg, client, path := tunnelTestService(t, backend.URL, config.SubRule{})
	req, _ := http.NewRequest("GET", "http://unpublished.example.com/", nil)
	req.Header.Set("CF-Connecting-IP", "203.0.113.11")
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("unpublished host accepted")
	}
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, service)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/sites/site", nil))
	if w.Code != 409 {
		t.Fatalf("referenced site deleted: %s", w.Body.String())
	}
	cfg.Update(func(c *config.Config) error { c.Sites[0].Enabled = false; return nil })
	service.Reload()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disabled site socket retained")
	}
}
