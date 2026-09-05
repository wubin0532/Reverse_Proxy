package webproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/http2"

	"andey-proxy/internal/config"
	"andey-proxy/internal/forward"
)

func TestProxyTransportPoolAndTimeouts(t *testing.T) {
	rule := config.SubRule{ConnectTimeout: 5, ResponseHeaderTimeout: 60}
	transport := proxyTransport(rule)
	defer transport.CloseIdleConnections()
	if transport.MaxIdleConnsPerHost != 32 || transport.IdleConnTimeout != 90*time.Second || !transport.ForceAttemptHTTP2 {
		t.Fatalf("unexpected transport pool: %#v", transport)
	}
	if transport.ResponseHeaderTimeout != 60*time.Second {
		t.Fatalf("response header timeout = %v", transport.ResponseHeaderTimeout)
	}
}

func TestReverseConnectionReuseAndResponseHeaderTimeout(t *testing.T) {
	var connections atomic.Int32
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	backend.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	backend.Start()
	defer backend.Close()

	h, err := newReverseHandler(config.SubRule{ID: "reuse", Name: "reuse", Backends: []string{backend.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://public.example/test", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Fatalf("request %d = %d %q", i, rec.Code, rec.Body.String())
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("backend connections = %d, want 1 reused connection", got)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		fmt.Fprint(w, "late")
	}))
	defer slow.Close()
	timed, err := newReverseHandler(config.SubRule{ID: "timeout", Name: "timeout", Backends: []string{slow.URL}, ResponseHeaderTimeout: 1}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://public.example/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	timed.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("timeout status = %d", rec.Code)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 1400*time.Millisecond {
		t.Fatalf("timeout elapsed = %v", elapsed)
	}
}

func TestStripPrefixPreservesBackendBaseAndQuery(t *testing.T) {
	var gotPath, gotQuery, gotPrefix string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotPrefix = r.Header.Get("X-Forwarded-Prefix")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	h, err := newReverseHandler(config.SubRule{
		ID: "strip", Name: "strip", Backends: []string{backend.URL + "/base"},
		FrontendPath: "/app", StripPrefix: true,
	}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://public.example/app/api?token=kept", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || gotPath != "/base/api" || gotQuery != "token=kept" || gotPrefix != "/app" {
		t.Fatalf("status/path/query/prefix = %d %q %q %q", rec.Code, gotPath, gotQuery, gotPrefix)
	}
}

func TestRewriteLocationAndCookiesIsScoped(t *testing.T) {
	target, _ := url.Parse("http://internal.local:8080/base")
	req := httptest.NewRequest(http.MethodGet, "https://public.example/app", nil)
	req = req.WithContext(context.WithValue(req.Context(), publicRequestKey{}, publicRequestInfo{scheme: "https", host: "public.example"}))
	res := &http.Response{Request: req, Header: make(http.Header)}
	res.Header.Set("Location", "http://internal.local:8080/login?next=%2F")
	res.Header.Add("Set-Cookie", "sid=abc; Domain=.internal.local; Path=/base; HttpOnly; SameSite=Lax")
	rewriteProxyResponse(res, target, config.SubRule{
		RewriteLocation: true, CookieDomainFrom: "internal.local", CookieDomainTo: "public.example",
		CookiePathFrom: "/base", CookiePathTo: "/app",
	})
	if got := res.Header.Get("Location"); got != "https://public.example/login?next=%2F" {
		t.Fatalf("rewritten location = %q", got)
	}
	cookie := res.Header.Get("Set-Cookie")
	for _, want := range []string{"Domain=public.example", "Path=/app", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Fatalf("rewritten cookie %q missing %q", cookie, want)
		}
	}

	res.Header.Set("Location", "https://unrelated.example/login")
	rewriteProxyResponse(res, target, config.SubRule{RewriteLocation: true})
	if got := res.Header.Get("Location"); got != "https://unrelated.example/login" {
		t.Fatalf("unrelated location changed to %q", got)
	}
}

func TestRateLimiterUsesDirectIPAndStaysBounded(t *testing.T) {
	limiter := newRuleLimiter()
	rule := &config.SubRule{ID: "rule", RateLimitRPS: 1, RateLimitBurst: 2}
	now := time.Now()
	if ok, _ := limiter.allow(rule, "127.0.0.1", now); !ok {
		t.Fatal("first request should pass")
	}
	if ok, _ := limiter.allow(rule, "127.0.0.1", now); !ok {
		t.Fatal("burst request should pass")
	}
	if ok, retry := limiter.allow(rule, "127.0.0.1", now); ok || retry != 1 {
		t.Fatalf("third request = %v retry %d", ok, retry)
	}
	if ok, _ := limiter.allow(rule, "127.0.0.2", now); !ok {
		t.Fatal("different direct IP should have its own bucket")
	}
	for i := 0; i < maxRateLimitBuckets+100; i++ {
		limiter.allow(rule, fmt.Sprintf("10.0.%d.%d", i/256, i%256), now.Add(time.Duration(i)*time.Nanosecond))
	}
	if got := len(limiter.buckets); got > maxRateLimitBuckets {
		t.Fatalf("bucket count = %d", got)
	}
}

func TestMaxRequestBodyRejectsKnownAndStreamingBodies(t *testing.T) {
	var backendHits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHits.Add(1)
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	rule := config.SubRule{ID: "body", Name: "body", Type: "reverse", Enabled: true, Backends: []string{backend.URL}, MaxRequestBodyMiB: 1}
	ss := &siteServer{site: config.Site{ID: "site", Rules: []config.SubRule{rule}}, logs: forward.NewRingLog(10), limiter: newRuleLimiter(), revHandler: make(map[string]http.Handler), revErr: make(map[string]error)}
	h := &siteHandler{ss: ss}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://public.example/upload", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	req.RemoteAddr = "127.0.0.1:1234"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || backendHits.Load() != 0 {
		t.Fatalf("known body = status %d hits %d", rec.Code, backendHits.Load())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "http://public.example/upload", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	req.ContentLength = -1
	req.RemoteAddr = "127.0.0.1:1234"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("streaming body status = %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestRuleHotReloadKeepsListenerAndInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	oldBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		fmt.Fprint(w, "old")
	}))
	defer oldBackend.Close()
	newBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "new") }))
	defer newBackend.Close()

	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{ID: "hot", Name: "hot", Enabled: true, Listen: "127.0.0.1:0", Rules: []config.SubRule{{ID: "r", Name: "r", Type: "reverse", Enabled: true, Backends: []string{oldBackend.URL}}}})
	svc.Start()
	addr := svc.ListenAddr("hot")
	type requestResult struct {
		body string
		err  error
	}
	result := make(chan requestResult, 1)
	go func() {
		resp, err := httpClient().Get("http://" + addr + "/slow")
		if err != nil {
			result <- requestResult{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		result <- requestResult{body: string(body), err: err}
	}()
	<-started
	cfg.Lock()
	cfg.Sites[0].Rules[0].Backends = []string{newBackend.URL}
	cfg.Unlock()
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := svc.ListenAddr("hot"); got != addr {
		t.Fatalf("listener changed from %s to %s", addr, got)
	}
	_, _, body := mustGet(t, httpClient(), "http://"+addr+"/new", nil)
	if body != "new" {
		t.Fatalf("new request body = %q", body)
	}
	close(release)
	if got := <-result; got.err != nil || got.body != "old" {
		t.Fatalf("inflight request = %q, %v", got.body, got.err)
	}
}

func TestTLSListenerSupportsHTTP2(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{ID: "h2", Name: "h2", Enabled: true, Listen: "127.0.0.1:0", TLS: true, Rules: []config.SubRule{{ID: "r", Name: "r", Type: "redirect", Enabled: true, RedirectURL: "https://example.com/"}}})
	svc.Start()
	transport := &http2.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}} // #nosec G402: local self-signed test
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get("https://" + svc.ListenAddr("h2") + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusFound {
		t.Fatalf("proto/status = %s %d", resp.Proto, resp.StatusCode)
	}
}

func TestReverseWebSocketAndSSEStreaming(t *testing.T) {
	websocketBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "upgrade required", http.StatusBadRequest)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unavailable", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		payload := make([]byte, 4)
		if _, err := io.ReadFull(conn, payload); err == nil {
			_, _ = conn.Write(payload)
		}
	}))
	defer websocketBackend.Close()
	h, err := newReverseHandler(config.SubRule{ID: "ws", Name: "ws", Backends: []string{websocketBackend.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(h)
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	conn, err := net.DialTimeout("tcp", proxyURL.Host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprintf(conn, "GET /socket HTTP/1.1\r\nHost: public.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("websocket status = %q, %v", status, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	_, _ = conn.Write([]byte("ping"))
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("websocket echo = %q, %v", echo, err)
	}

	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	sseBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer sseBackend.Close()
	sseProxy, err := newReverseHandler(config.SubRule{ID: "sse", Name: "sse", Backends: []string{sseBackend.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	sseFront := httptest.NewServer(sseProxy)
	defer sseFront.Close()
	start := time.Now()
	resp, err := httpClient().Get(sseFront.URL)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: one\n" {
		t.Fatalf("SSE first line = %q, %v", line, err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("SSE was buffered for %v", elapsed)
	}
	close(release)
	_ = resp.Body.Close()
}

func TestBackendConnectionTestDoesNotSendHTTPOrLeakURL(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer backend.Close()
	h := &apiHandler{}

	body := fmt.Sprintf(`{"url":%q,"connectTimeoutSeconds":2,"skipBackendTlsVerify":true}`, backend.URL+"/private?token=CANARY")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sites/backend-test", strings.NewReader(body))
	h.testBackend(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tcp":true`) || !strings.Contains(rec.Body.String(), `"tls":true`) {
		t.Fatalf("backend test = %d %s", rec.Code, rec.Body.String())
	}
	if requests.Load() != 0 {
		t.Fatalf("backend test sent %d HTTP requests", requests.Load())
	}

	body = fmt.Sprintf(`{"url":%q,"connectTimeoutSeconds":2}`, backend.URL+"/private?token=CANARY")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/sites/backend-test", strings.NewReader(body))
	h.testBackend(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("strict TLS status = %d", rec.Code)
	}
	if text := rec.Body.String(); strings.Contains(text, "CANARY") || strings.Contains(text, backend.URL) || strings.Contains(text, "/private") {
		t.Fatalf("backend test leaked URL: %s", text)
	}
}

func TestListenerChangeFailureRollsBackConfigAndService(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	originalAddr := reserved.Addr().String()
	_ = reserved.Close()

	cfg, svc := newTestService(t)
	original := config.Site{ID: "rollback", Name: "old", Enabled: true, Listen: originalAddr, Rules: []config.SubRule{{ID: "r", Name: "r", Type: "redirect", Enabled: true, RedirectURL: "https://old.example/"}}}
	addSite(cfg, original)
	svc.Start()
	assertReachable(t, originalAddr, true)

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	changed := original
	changed.Name = "new"
	changed.Listen = occupied.Addr().String()
	payload, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", original.ID)
	req := httptest.NewRequest(http.MethodPut, "/api/sites/"+original.ID, bytes.NewReader(payload))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	rec := httptest.NewRecorder()
	(&apiHandler{cfg: cfg, svc: svc}).update(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	cfg.RLock()
	got := cfg.Sites[0]
	cfg.RUnlock()
	if got.Name != original.Name || got.Listen != original.Listen {
		t.Fatalf("config was not rolled back: %#v", got)
	}
	assertReachable(t, originalAddr, true)
}
