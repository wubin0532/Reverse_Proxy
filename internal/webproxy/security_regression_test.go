package webproxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func securityTestReverseHandler(t *testing.T, rule config.SubRule) http.Handler {
	t.Helper()
	h, e := newReverseHandler(rule, newTestRingLog())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(h.(*reverseHandler).closeIdleConnections)
	return h
}
func TestSecurityRegressionFailoverPreservesRequest(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s host=%s forwarded=%s", r.URL.RequestURI(), r.Host, r.Header.Get("X-Forwarded-Host"))
	}))
	defer b.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + l.Addr().String()
	l.Close()
	h := securityTestReverseHandler(t, config.SubRule{Backends: []string{dead + "/base?a=1", b.URL + "/base?a=1"}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://public.example/item?q=2", nil))
	want := "/base/item?a=1&q=2 host=" + b.Listener.Addr().String() + " forwarded=public.example"
	if w.Body.String() != want {
		t.Fatalf("wanted %q; observed %q", want, w.Body.String())
	}
}
func TestSecurityRegressionEmptyPOSTNotReplayed(t *testing.T) {
	var count atomic.Int32
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); fmt.Fprint(w, "ok") }))
	defer b.Close()
	h := securityTestReverseHandler(t, config.SubRule{Backends: []string{a.URL, b.URL}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "http://public.example/execute", nil))
	if count.Load() != 1 {
		t.Fatalf("single POST executed %d times, status=%d", count.Load(), w.Code)
	}
}

type sniffSignalListener struct {
	net.Listener
	accepted chan struct{}
}

func (l *sniffSignalListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	select {
	case l.accepted <- struct{}{}:
	default:
	}
	return c, e
}
func TestSecurityRegressionSilentClientDoesNotBlockAccept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sig := &sniffSignalListener{ln, make(chan struct{}, 4)}
	sl := &tlsSniffListener{Listener: sig, tlsConfig: &tls.Config{}}
	defer sl.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := sl.Accept()
		if e == nil {
			c.Close()
		}
	}()
	silent, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	<-sig.accepted
	active, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	fmt.Fprint(active, "GET / HTTP/1.1\r\nHost: example\r\n\r\n")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("active client blocked behind silent client inside Accept")
	}
}
func TestSecurityRegressionStaleSnapshotDoesNotPoisonCache(t *testing.T) {
	old := config.Site{Rules: []config.SubRule{{ID: "r", Type: "redirect", Enabled: true, RedirectURL: "https://old.example"}}}
	ss := &siteServer{site: old}
	snapshot := ss.siteSnapshot()
	next := cloneSite(old)
	next.Rules[0].RedirectURL = "https://new.example"
	ss.updateSite(next)
	ss.staticHandlerFor(&snapshot.Rules[0])
	current := ss.siteSnapshot()
	w := httptest.NewRecorder()
	ss.staticHandlerFor(&current.Rules[0]).ServeHTTP(w, httptest.NewRequest("GET", "http://example/", nil))
	if got := w.Header().Get("Location"); got != "https://new.example" {
		t.Fatalf("updated configuration serves stale Location %q", got)
	}
}
func TestSecurityRegressionFileServerConfinesSymlinks(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "public")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(d, "secret")
	if err := os.WriteFile(secret, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	fileServerHandler(config.SubRule{RootDir: root}).ServeHTTP(w, httptest.NewRequest("GET", "http://example/link", nil))
	if w.Code == 200 {
		t.Fatalf("root escape served %q", w.Body.String())
	}
}
func TestSecurityRegressionPathGuardCanonicalization(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Clean(r.URL.Path) == "/admin/secret" {
			fmt.Fprint(w, "protected-data")
		}
	}))
	defer b.Close()
	ss := &siteServer{site: config.Site{Rules: []config.SubRule{
		{ID: "public", Type: "reverse", Enabled: true, FrontendPath: "/", Backends: []string{b.URL}},
		{ID: "admin", Type: "reverse", Enabled: true, FrontendPath: "/admin", Backends: []string{b.URL}, BasicAuth: true, AuthUser: "admin", AuthPass: "secret"},
	}}, logs: newTestRingLog(), limiter: newRuleLimiter(), revHandler: make(map[string]http.Handler), revErr: make(map[string]error)}
	defer ss.closeIdleConnections()
	for _, p := range []string{"/admin/secret", "/public/../admin/secret", "//admin/secret", "/public/%2e%2e/admin/secret", "/%2fadmin/secret", "/public/./../admin/secret"} {
		w := httptest.NewRecorder()
		(&siteHandler{ss}).ServeHTTP(w, httptest.NewRequest("GET", "http://example"+p, nil))
		if w.Code != 401 && w.Code != 400 {
			t.Errorf("path %q bypassed auth: %d %q", p, w.Code, w.Body.String())
		}
	}
}
func TestSecurityRegressionStripPrefixPreservesEscapedSlash(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.RequestURI) }))
	defer b.Close()
	h := securityTestReverseHandler(t, config.SubRule{FrontendPath: "/api", StripPrefix: true, Backends: []string{b.URL}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://example/api/a%2Fb", nil))
	if w.Body.String() != "/a%2Fb" {
		t.Fatalf("escaped segment changed to %q", w.Body.String())
	}
}

func TestFailoverWithStripPrefixAndBodyLimit(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s %s", r.RequestURI, r.Host, r.Header.Get("X-Forwarded-Host"))
	}))
	defer b.Close()
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadURL := "http://" + dead.Addr().String()
	dead.Close()
	rule := config.SubRule{ID: "r", Enabled: true, Type: "reverse", FrontendPath: "/api", StripPrefix: true, PreserveHost: true, MaxRequestBodyMiB: 1, Backends: []string{deadURL + "/base?b=1", b.URL + "/base?b=1"}}
	ss := &siteServer{site: config.Site{Rules: []config.SubRule{rule}}, logs: newTestRingLog(), limiter: newRuleLimiter(), revHandler: make(map[string]http.Handler), revErr: make(map[string]error)}
	defer ss.closeIdleConnections()
	w := httptest.NewRecorder()
	(&siteHandler{ss}).ServeHTTP(w, httptest.NewRequest("GET", "http://public.example/api/a%2Fb?q=2", nil))
	if got, want := w.Body.String(), "/base/a%2Fb?b=1&q=2 public.example public.example"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStripPrefixEncodedBoundaryAndTrailingSlash(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.RequestURI) }))
	defer b.Close()
	for _, tc := range []struct{ prefix, request, want string }{
		{"/api", "/api%2Fa%2Fb", "/base/a%2Fb"},
		{"/api/", "/api/a%2Fb", "/base/a%2Fb"},
		{"/api", "/%61pi/a%2Fb", "/base/a%2Fb"},
		{"/api", "/api", "/base/"},
		{"/api", "/api/a%25b", "/base/a%25b"},
	} {
		h := securityTestReverseHandler(t, config.SubRule{FrontendPath: tc.prefix, StripPrefix: true, Backends: []string{b.URL + "/base"}})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://example"+tc.request, nil))
		if got := w.Body.String(); got != tc.want {
			t.Errorf("%s (%s): got %q want %q", tc.request, tc.prefix, got, tc.want)
		}
	}
}

func TestUpdatePreservesUnchangedPoolAndRejectsOldReverseRule(t *testing.T) {
	site := config.Site{Rules: []config.SubRule{
		{ID: "keep", Type: "reverse", Backends: []string{"http://127.0.0.1:1"}},
		{ID: "change", Type: "reverse", Backends: []string{"http://127.0.0.1:2"}},
	}}
	ss := &siteServer{site: site, logs: newTestRingLog(), revHandler: make(map[string]http.Handler), revErr: make(map[string]error)}
	defer ss.closeIdleConnections()
	keep, err := ss.reverseHandlerFor(&site.Rules[0])
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ss.reverseHandlerFor(&site.Rules[1])
	if err != nil {
		t.Fatal(err)
	}
	next := cloneSite(site)
	next.Rules[1].Backends = []string{"http://127.0.0.1:3"}
	ss.updateSite(next)
	if _, err := ss.reverseHandlerFor(&site.Rules[1]); err != errRuleUpdated {
		t.Fatalf("stale rule accepted: %v", err)
	}
	current := ss.siteSnapshot()
	got, err := ss.reverseHandlerFor(&current.Rules[0])
	if err != nil || got != keep {
		t.Fatal("unchanged connection pool discarded")
	}
	got, err = ss.reverseHandlerFor(&current.Rules[1])
	if err != nil || got == changed {
		t.Fatal("changed backend retained old handler")
	}
}

func TestFileServerAllowsInternalSymlinkAndRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("visible"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("visible.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"visible.txt", "link"} {
		w := httptest.NewRecorder()
		fileServerHandler(config.SubRule{RootDir: root, FrontendPath: "/files"}).ServeHTTP(w, httptest.NewRequest("GET", "http://example/files/"+name, nil))
		if w.Code != 200 || w.Body.String() != "visible" {
			t.Fatalf("%s: %d %q", name, w.Code, w.Body.String())
		}
	}
}

func TestSniffPendingLimitAndClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := &tlsSniffListener{Listener: ln, tlsConfig: &tls.Config{}}
	defer sl.Close()
	done := make(chan error, 1)
	go func() {
		c, e := sl.Accept()
		if c != nil {
			c.Close()
		}
		done <- e
	}()
	for i := 0; i < maxPendingSniffs; i++ {
		c, e := net.Dial("tcp", ln.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
	}
	sl.init()
	deadline := time.Now().Add(3 * time.Second)
	for {
		sl.mu.Lock()
		n := len(sl.pending)
		sl.mu.Unlock()
		if n == maxPendingSniffs {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d pending sniffs", n)
		}
		time.Sleep(time.Millisecond)
	}
	extra, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil {
		t.Fatal("over-budget connection accepted")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("over-budget connection left open")
	}
	sl.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Accept returned no close error")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Accept")
	}
	deadline = time.Now().Add(time.Second)
	for {
		sl.mu.Lock()
		n := len(sl.pending)
		sl.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d pending sniffs leaked", n)
		}
		time.Sleep(time.Millisecond)
	}
}

type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "temporary accept failure" }
func (temporaryAcceptError) Timeout() bool   { return false }
func (temporaryAcceptError) Temporary() bool { return true }

type transientListener struct {
	net.Listener
	failed bool
}

func (l *transientListener) Accept() (net.Conn, error) {
	if !l.failed {
		l.failed = true
		return nil, temporaryAcceptError{}
	}
	return l.Listener.Accept()
}
func TestSniffRecoversTemporaryAcceptError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := &tlsSniffListener{Listener: &transientListener{Listener: ln}, tlsConfig: &tls.Config{}}
	defer sl.Close()
	done := make(chan error, 1)
	go func() {
		c, e := sl.Accept()
		if c != nil {
			c.Close()
		}
		done <- e
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: example\r\n\r\n")
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("temporary error stopped listener: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not retry Accept")
	}
}
