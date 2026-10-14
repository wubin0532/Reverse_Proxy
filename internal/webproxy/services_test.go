package webproxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/guard"
)

func serviceFixture(t *testing.T, rule config.SubRule) (*config.Config, *Service, string) {
	t.Helper()
	cfg, e := config.Load(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cfg.State().Close() })
	if e = cfg.Update(func(c *config.Config) error {
		c.Sites = []config.Site{{ID: "site", Name: "Services", Listen: "127.0.0.1:0", Enabled: true, Rules: []config.SubRule{rule}}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	svc := NewService(cfg, nil)
	svc.Start()
	t.Cleanup(svc.Stop)
	return cfg, svc, "http://" + svc.ListenAddr("site")
}
func requestService(t *testing.T, method, url, body string, auth bool, headers map[string]string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if auth {
		req.SetBasicAuth("fixture", "secret")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, e := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, res.Header, string(b)
}
func TestWebDAVReadWriteAuthenticationAndRootConfinement(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("external-secret"), 0600)
	os.WriteFile(filepath.Join(dir, "hello"), []byte("hello"), 0600)
	if e := os.Symlink(outside, filepath.Join(dir, "escape")); e != nil {
		t.Fatal(e)
	}
	rule := config.SubRule{ID: "dav", Name: "DAV", Type: "webdav", Enabled: true, RootDir: dir, FrontendPath: "/dav", WebDAVWritable: true, BasicAuth: true, AuthUser: "fixture", AuthPass: "secret"}
	cfg, svc, u := serviceFixture(t, rule)
	if code, _, _ := requestService(t, "PUT", u+"/dav/new", "payload", false, nil); code != 401 {
		t.Fatalf("unauthenticated write: %d", code)
	}
	if code, _, body := requestService(t, "PUT", u+"/dav/new", "payload", true, nil); code != 201 {
		t.Fatalf("write: %d %s", code, body)
	}
	if code, _, body := requestService(t, "GET", u+"/dav/new", "", true, nil); code != 200 || body != "payload" {
		t.Fatal(code, body)
	}
	if code, _, body := requestService(t, "PROPFIND", u+"/dav/", "", true, map[string]string{"Depth": "1"}); code != 207 || !strings.Contains(body, "/dav/new") {
		t.Fatal(code, body)
	}
	if code, _, _ := requestService(t, "PROPFIND", u+"/dav/", "", true, map[string]string{"Depth": "infinity"}); code != 403 {
		t.Fatal("unbounded traversal", code)
	}
	if code, _, body := requestService(t, "GET", u+"/dav/escape/secret", "", true, nil); code == 200 || strings.Contains(body, "external-secret") {
		t.Fatal("symlink escaped root", code, body)
	}
	if code, _, _ := requestService(t, "PUT", u+"/dav/escape/new", "bad", true, nil); code < 400 {
		t.Fatal("write escaped root", code)
	}
	if _, e := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(e) {
		t.Fatal("outside changed", e)
	}
	if e := cfg.Update(func(c *config.Config) error { c.Sites[0].Rules[0].WebDAVWritable = false; return nil }); e != nil {
		t.Fatal(e)
	}
	if e := svc.Reload(); e != nil {
		t.Fatal(e)
	}
	if code, _, _ := requestService(t, "DELETE", u+"/dav/new", "", true, nil); code != 403 {
		t.Fatal("read-only delete", code)
	}
	if code, _, body := requestService(t, "GET", u+"/dav/new", "", true, nil); code != 200 || body != "payload" {
		t.Fatal("read-only damaged file", code, body)
	}
}
func TestCGIFixedExecutableAuthenticationSecretsAndTimeout(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("CGI fd execution unavailable")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf 'Content-Type: text/plain\\nSet-Cookie: andey-access-test=evil\\nSet-Cookie: business=ok\\n\\n'\nprintf '%s|%s|%s|%s|%s' \"$REQUEST_METHOD\" \"$PATH_INFO\" \"$HTTP_AUTHORIZATION\" \"$HTTP_COOKIE\" \"${CODEX_HOME:-unset}\"\n"
	if e := os.WriteFile(filepath.Join(dir, "app.cgi"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	rule := config.SubRule{ID: "cgi", Name: "CGI", Type: "cgi", Enabled: true, RootDir: dir, CGIEntry: "app.cgi", CGITimeoutSeconds: 1, FrontendPath: "/cgi", BasicAuth: true, AuthUser: "fixture", AuthPass: "secret"}
	_, _, u := serviceFixture(t, rule)
	if code, _, _ := requestService(t, "GET", u+"/cgi/other.cgi", "", false, nil); code != 401 {
		t.Fatal("CGI unauthenticated", code)
	}
	code, headers, body := requestService(t, "GET", u+"/cgi/other.cgi?q=x", "", true, map[string]string{"Cookie": api.TokenCookie + "=private; andey-access-test=private; business=ok"})
	if code != 200 || body != "GET|/other.cgi||business=ok|unset" {
		t.Fatal("CGI routing/secret leak", code, body)
	}
	if values := headers.Values("Set-Cookie"); len(values) != 1 || !strings.Contains(values[0], "business=ok") {
		t.Fatal("CGI reserved cookie escaped", values)
	}
	if e := os.WriteFile(filepath.Join(dir, "app.cgi"), []byte("#!/bin/sh\nexec sleep 5\n"), 0700); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if code, _, _ := requestService(t, "GET", u+"/cgi/", "", true, nil); code != 504 {
		t.Fatal("timeout", code)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("CGI timeout did not release process")
	}
	for _, bad := range []config.SubRule{{Type: "cgi", RootDir: dir, CGIEntry: "../bad", BasicAuth: true}, {Type: "cgi", RootDir: dir, CGIEntry: "app.cgi"}, {Type: "webdav", RootDir: dir, WebDAVWritable: true}, {Type: "webdav", RootDir: "/"}} {
		if bad.ValidateAdvanced() == nil {
			t.Fatalf("unsafe service accepted: %+v", bad)
		}
	}
}
func TestDiscoveryExplicitBoundsActualTCPAndShutdown(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			conn.Close()
		}
	}()
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	d := &discoveryRuntime{}
	j, e := d.start(discoverySpec{Targets: []string{"127.0.0.1"}, Ports: []int{port}, TimeoutMs: 100})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		j, _ = d.snapshot(j.ID)
		if j.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan did not complete")
		}
		time.Sleep(time.Millisecond * 5)
	}
	if j.Status != "completed" || j.Completed != 1 || len(j.Results) != 1 || j.Results[0].Address != listener.Addr().String() {
		t.Fatal(j)
	}
	for _, spec := range []discoverySpec{{Targets: []string{"192.168.1.0/16"}, Ports: []int{80}}, {Targets: []string{"8.8.8.8"}, Ports: []int{80}}, {Targets: []string{"localhost"}, Ports: []int{80}}, {Targets: []string{"127.0.0.1"}, Ports: []int{80, 80}}, {Targets: []string{"::/0"}, Ports: []int{80}}} {
		if _, e := discoveryTargets(&spec); e == nil {
			t.Fatal("invalid discovery scope accepted", spec)
		}
	}
	d.stop()
	if _, e := d.start(discoverySpec{Targets: []string{"127.0.0.1"}, Ports: []int{port}}); e == nil {
		t.Fatal("shutdown admitted new scan")
	}
	listener.Close()
	<-done
}
func TestProxyRemovesAuthenticationAndReservedCookies(t *testing.T) {
	received := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.Header().Add("Set-Cookie", api.TokenCookie+"=evil")
		w.Header().Add("Set-Cookie", "andey-access-other=evil")
		w.Header().Add("Set-Cookie", "business=ok")
		io.WriteString(w, "ok")
	}))
	defer backend.Close()
	rule := config.SubRule{ID: "r", Name: "r", Type: "reverse", Enabled: true, Backends: []string{backend.URL}, BasicAuth: true, AuthUser: "fixture", AuthPass: "secret"}
	_, _, u := serviceFixture(t, rule)
	code, headers, body := requestService(t, "GET", u+"/", "", true, map[string]string{"Cookie": api.TokenCookie + "=private; andey-access-other=private; business=ok"})
	if code != 200 || body != "ok" {
		t.Fatal(code, body)
	}
	upstream := <-received
	if upstream.Get("Authorization") != "" || upstream.Get("Cookie") != "business=ok" {
		t.Fatal("credential forwarded", upstream)
	}
	if values := headers.Values("Set-Cookie"); len(values) != 1 || !bytes.Contains([]byte(values[0]), []byte("business=ok")) {
		t.Fatal("reserved cookie escaped", values)
	}
}

func TestLiveHTTPDefenseBoundPolicies(t *testing.T) {
	backendCalls := atomic.Int64{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backendCalls.Add(1); io.WriteString(w, "ok") }))
	defer backend.Close()
	rule := config.SubRule{ID: "rule", Name: "App", Type: "reverse", Enabled: true, PolicyID: "defense", Backends: []string{backend.URL}}
	cfg, e := config.Load(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer cfg.State().Close()
	if e = cfg.Update(func(c *config.Config) error {
		c.ProtectionPolicies = []config.ProtectionPolicy{{ID: "defense", Name: "Defense", Enabled: true, Global: true, WAF: &config.WAFConfig{Enabled: true, BodyLimitKiB: 16}}}
		c.Sites = []config.Site{{ID: "site", Name: "App", Listen: "127.0.0.1:0", Enabled: true, PolicyID: "defense", Rules: []config.SubRule{rule}}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	protection := guard.NewEngine(cfg)
	svc := NewService(cfg, nil)
	svc.Protection = protection
	svc.Start()
	defer svc.Stop()
	u := "http://" + svc.ListenAddr("site")
	headers := map[string]string{"User-Agent": "Mozilla/5.0", "Accept": "text/plain"}
	if code, _, _ := requestService(t, "GET", u+"/?q=normal", "", false, headers); code != 200 {
		t.Fatal("normal request", code)
	}
	attack := "/?q=" + url.QueryEscape("<script>alert(1)</script>")
	if code, _, _ := requestService(t, "GET", u+attack, "", false, headers); code != 403 {
		t.Fatal("bound WAF not enforced", code)
	}
	if backendCalls.Load() != 1 {
		t.Fatal("WAF denial reached backend")
	}
	if e = cfg.Update(func(c *config.Config) error {
		c.ProtectionPolicies[0].WAF.DetectionOnly = true
		c.ProtectionPolicies[0].CC = &config.CCConfig{Enabled: true, WindowSeconds: 60, MaxRequests: 2}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = protection.Reload(); e != nil {
		t.Fatal(e)
	}
	if code, _, _ := requestService(t, "GET", u+attack, "", false, headers); code != 200 {
		t.Fatal("observation blocked", code)
	}
	if code, _, _ := requestService(t, "GET", u+"/", "", false, headers); code != 200 {
		t.Fatal("duplicate policy counted twice", code)
	}
	if code, _, _ := requestService(t, "GET", u+"/", "", false, headers); code != 429 {
		t.Fatal("CC quota ignored", code)
	}
	if backendCalls.Load() != 3 {
		t.Fatal("CC denial reached backend", backendCalls.Load())
	}
	stats := svc.AllSiteStats()["site"].Rules["rule"]
	if stats.Requests != 5 || stats.Status4xx != 2 || stats.Active != 0 {
		t.Fatal("WAF/CC denials missing from rule statistics", stats)
	}
}

func TestWebDAVAtomicUploadAndCopyPreserveExistingFile(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "existing"), []byte("keep previous contents"), 0600); e != nil {
		t.Fatal(e)
	}
	rule := config.SubRule{ID: "dav", Name: "DAV", Type: "webdav", Enabled: true, RootDir: dir, FrontendPath: "/dav", WebDAVWritable: true, MaxRequestBodyMiB: 1, BasicAuth: true, AuthUser: "fixture", AuthPass: "secret"}
	_, _, u := serviceFixture(t, rule)
	request, _ := http.NewRequest("PUT", u+"/dav/existing", io.NopCloser(strings.NewReader(strings.Repeat("x", (1<<20)+8192))))
	request.SetBasicAuth("fixture", "secret")
	res, e := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatal("streaming upload limit", res.StatusCode)
	}
	preserved, e := os.ReadFile(filepath.Join(dir, "existing"))
	if e != nil || string(preserved) != "keep previous contents" {
		t.Fatal("oversized upload changed destination", e, string(preserved))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("failed upload retained temporary files", entries)
	}
	if code, _, body := requestService(t, "PUT", u+"/dav/existing", "complete replacement", true, nil); code != 201 {
		t.Fatal(code, body)
	}
	if code, _, body := requestService(t, "GET", u+"/dav/existing", "", true, nil); code != 200 || body != "complete replacement" {
		t.Fatal(code, body)
	}
	if code, _, body := requestService(t, "PUT", u+"/dav/source", "copied file", true, nil); code != 201 {
		t.Fatal(code, body)
	}
	if code, _, body := requestService(t, "COPY", u+"/dav/source", "", true, map[string]string{"Destination": u + "/dav/existing", "Depth": "0", "Overwrite": "T"}); code != 204 {
		t.Fatal("copy", code, body)
	}
	if code, _, body := requestService(t, "GET", u+"/dav/existing", "", true, nil); code != 200 || body != "copied file" {
		t.Fatal(code, body)
	}
	if code, _, body := requestService(t, "MOVE", u+"/dav/source", "", true, map[string]string{"Destination": u + "/dav/existing", "Overwrite": "T"}); code != 204 {
		t.Fatal("move", code, body)
	}
	if _, e := os.Stat(filepath.Join(dir, "source")); !os.IsNotExist(e) {
		t.Fatal("move did not retire source", e)
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	state := &davWriteState{}
	ctx := context.WithValue(context.Background(), davWriteKey{}, state)
	file, e := (rootedDAV{root}).OpenFile(ctx, "existing", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if e != nil {
		t.Fatal(e)
	}
	file.Write([]byte("incomplete"))
	state.readErr = io.ErrUnexpectedEOF
	if file.Close() == nil {
		t.Fatal("incomplete file committed")
	}
	preserved, _ = os.ReadFile(filepath.Join(dir, "existing"))
	if string(preserved) != "copied file" {
		t.Fatal("interrupted upload lost old file", string(preserved))
	}
}
