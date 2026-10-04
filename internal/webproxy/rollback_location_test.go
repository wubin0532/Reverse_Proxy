package webproxy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/config"
)

func TestStripPrefixLocationRoundTrip(t *testing.T) {
	var backendURL string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", backendURL+"/login")
		w.WriteHeader(302)
	}))
	defer backend.Close()
	backendURL = backend.URL
	h, err := newReverseHandler(config.SubRule{ID: "r", Name: "r", Type: "reverse", Enabled: true, FrontendPath: "/app", StripPrefix: true, RewriteLocation: true, Backends: []string{backend.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	defer h.(*reverseHandler).shutdown()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://public.example/app/start", nil))
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Path != "/app/login" {
		t.Fatalf("redirect leaves the mounted route: got %s, want /app/login", location)
	}
}

func TestConcurrentRollbackPreservesWorkingConfig(t *testing.T) {
	cfg, svc := newTestService(t)
	original := config.Site{ID: "s", Name: "site", Enabled: true, Listen: freeRuleAPIAddr(t), Rules: []config.SubRule{{ID: "r", Name: "rule", Type: "redirect", Enabled: true, RedirectURL: "https://example.com"}}}
	addSite(cfg, original)
	svc.Start()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, svc)
	next := cloneSite(original)
	next.Listen = occupied.Addr().String()
	payload, _ := json.Marshal(next)
	svc.mu.Lock()
	a := make(chan *httptest.ResponseRecorder, 1)
	b := make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- callWebAPI(t, router, http.MethodPut, "/api/sites/s", string(payload)) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		cfg.RLock()
		saved := cfg.Sites[0].Listen == next.Listen
		cfg.RUnlock()
		if saved {
			if !svc.reloadMu.TryLock() {
				break
			}
			svc.reloadMu.Unlock()
		}
		if time.Now().After(deadline) {
			svc.mu.Unlock()
			t.Fatal("first mutation not reached")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { b <- callWebAPI(t, router, http.MethodPost, "/api/sites/s/rules/r/toggle", "") }()
	// Give the overlapping request a chance to reach the mutation lock. It
	// must not persist a new snapshot while A is still applying its change.
	time.Sleep(50 * time.Millisecond)
	svc.mu.Unlock()
	ar, br := <-a, <-b
	cfg.RLock()
	final := cloneSite(cfg.Sites[0])
	cfg.RUnlock()
	t.Logf("update=%d, toggle=%d, restoredListen=%t, enabled=%t", ar.Code, br.Code, final.Listen == original.Listen, final.Rules[0].Enabled)
	if final.Listen != original.Listen {
		t.Fatal("concurrent rollback restored the failed listener instead of the working listener")
	}
	if ar.Code != 409 || br.Code != 200 || final.Rules[0].Enabled {
		t.Fatal("successful rule toggle must survive the failed site update")
	}
	status, _, _ := mustGet(t, httpClient(), "http://"+svc.ListenAddr("s"), nil)
	if status != 404 {
		t.Fatalf("disabled rule is still reachable: %d", status)
	}
}

func TestLocationReversePathMapping(t *testing.T) {
	cases := []struct {
		name, target, location, frontend, want string
		strip                                  bool
	}{
		{"root mount", "http://backend", "http://backend/login?next=%2F#top", "/app", "https://public.example/app/login?next=%2F#top", true},
		{"backend subtree", "http://backend/base/", "http://backend/base/login", "/app/", "https://public.example/app/login", true},
		{"encoded slash", "http://backend/base", "http://backend/base/a%2Fb?x=%26", "/app", "https://public.example/app/a%2Fb?x=%26", true},
		{"unicode prefix", "http://backend/base", "http://backend/base/a%2Fb", "/应用", "https://public.example/%E5%BA%94%E7%94%A8/a%2Fb", true},
		{"encoded base", "http://backend/b%2Fase", "http://backend/b%2Fase/login", "/app", "https://public.example/app/login", true},
		{"outside subtree", "http://backend/base", "http://backend/baseball/login", "/app", "https://public.example/baseball/login", true},
		{"external host", "http://backend/base", "https://other.example/base/login", "/app", "https://other.example/base/login", true},
		{"relative location", "http://backend/base", "/base/login", "/app", "/base/login", true},
		{"no prefix stripping", "http://backend/base", "http://backend/base/login", "/app", "https://public.example/base/login", false},
		{"ambiguous path", "http://backend/base", "http://backend/base/../admin", "/app", "https://public.example/base/../admin", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := url.Parse(tt.target)
			req := httptest.NewRequest("GET", "https://public.example/app/start", nil)
			req = req.WithContext(context.WithValue(req.Context(), publicRequestKey{}, publicRequestInfo{scheme: "https", host: "public.example"}))
			res := &http.Response{Request: req, Header: make(http.Header)}
			res.Header.Set("Location", tt.location)
			rewriteProxyResponse(res, target, config.SubRule{FrontendPath: tt.frontend, StripPrefix: tt.strip, RewriteLocation: true})
			if got := res.Header.Get("Location"); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
