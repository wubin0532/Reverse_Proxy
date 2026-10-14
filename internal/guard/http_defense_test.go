package guard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func wafFixture(t *testing.T, observe bool) (*config.Config, *Engine) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfg.State().Close() })
	if err := cfg.Update(func(c *config.Config) error {
		c.ProtectionPolicies = []config.ProtectionPolicy{{ID: "waf", Name: "WAF", Enabled: true, WAF: &config.WAFConfig{Enabled: true, DetectionOnly: observe, BodyLimitKiB: 16}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(cfg)
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
	return cfg, e
}
func wafRequest(e *Engine, r *http.Request) (bool, *httptest.ResponseRecorder) {
	r.Header.Set("User-Agent", "Mozilla/5.0")
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	return e.InspectHTTP(w, r, "203.0.113.7", "rule", []string{"waf"}), w
}
func TestCorazaCRSBlockingAndObservation(t *testing.T) {
	cfg, e := wafFixture(t, false)
	if ok, w := wafRequest(e, httptest.NewRequest("GET", "http://app.example/?q=normal", nil)); !ok {
		t.Fatal("normal request rejected", w.Code, w.Body.String())
	}
	malicious := "http://app.example/?q=" + url.QueryEscape("<script>alert(1)</script>")
	if ok, w := wafRequest(e, httptest.NewRequest("GET", malicious, nil)); ok || w.Code != 403 {
		t.Fatal("CRS XSS was not blocked", ok, w.Code)
	}
	if err := cfg.Update(func(c *config.Config) error { c.ProtectionPolicies[0].WAF.DetectionOnly = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
	if ok, w := wafRequest(e, httptest.NewRequest("GET", malicious, nil)); !ok {
		t.Fatal("observation mode blocked", w.Code)
	}
	events := e.Events()
	if len(events) < 2 || events[0].Action != "observe" {
		t.Fatal(events)
	}
	for _, event := range events {
		if strings.Contains(event.Reason, "script") || strings.Contains(event.Reason, "alert(1)") {
			t.Fatal("WAF logged raw payload")
		}
	}
}
func TestWAFBodyPreservationAndCapacity(t *testing.T) {
	cfg, e := wafFixture(t, false)
	body := `{"message":"normal request"}`
	request := httptest.NewRequest("POST", "http://app.example/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if ok, w := wafRequest(e, request); !ok {
		t.Fatal(w.Code, w.Body.String())
	}
	read, err := io.ReadAll(request.Body)
	request.Body.Close()
	if err != nil || string(read) != body {
		t.Fatal("inspection consumed body", string(read), err)
	}
	large := strings.Repeat("x", 20<<10)
	request = httptest.NewRequest("POST", "http://app.example/", strings.NewReader(large))
	request.Header.Set("Content-Type", "text/plain")
	if ok, w := wafRequest(e, request); ok || w.Code != 413 {
		t.Fatal("WAF body budget", ok, w.Code)
	}
	cfg.Update(func(c *config.Config) error { c.ProtectionPolicies[0].WAF.DetectionOnly = true; return nil })
	e.Reload()
	request = httptest.NewRequest("POST", "http://app.example/", strings.NewReader(large))
	request.Header.Set("Content-Type", "text/plain")
	if ok, w := wafRequest(e, request); !ok {
		t.Fatal("observation oversized-body rejected", w.Code)
	}
	read, _ = io.ReadAll(request.Body)
	request.Body.Close()
	if string(read) != large {
		t.Fatal("observation truncated forwarded body")
	}
}
func TestCCWindowAnd404Ban(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	cfg.Update(func(c *config.Config) error {
		c.ProtectionPolicies = []config.ProtectionPolicy{{ID: "cc", Name: "CC", Enabled: true, Global: true, CC: &config.CCConfig{Enabled: true, WindowSeconds: 10, MaxRequests: 2, BanSeconds: 10, NotFoundThreshold: 2}}}
		return nil
	})
	e := NewEngine(cfg)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		if !e.InspectHTTP(w, httptest.NewRequest("GET", "http://app.example/", nil), "203.0.113.8", "r", []string{"cc", "cc"}) {
			t.Fatal("duplicate policy counted twice", i)
		}
	}
	w := httptest.NewRecorder()
	if e.InspectHTTP(w, httptest.NewRequest("GET", "http://app.example/", nil), "203.0.113.8", "r", nil) || w.Code != 429 || len(e.Bans()) != 1 {
		t.Fatal("CC limit not enforced")
	}
	e.ObserveHTTPStatus("203.0.113.9", "r", 404, nil)
	e.ObserveHTTPStatus("203.0.113.9", "r", 200, nil)
	e.ObserveHTTPStatus("203.0.113.9", "r", 404, nil)
	if len(e.Bans()) != 1 {
		t.Fatal("404 sequence did not reset")
	}
	e.ObserveHTTPStatus("203.0.113.9", "r", 404, nil)
	if len(e.Bans()) != 2 {
		t.Fatal("404 threshold did not ban")
	}
	p := e.snapshot.Load().policies["cc"]
	if !e.checkCC(p, "203.0.113.8", "r", time.Now().Add(11*time.Second)) {
		t.Fatal("CC window did not reset")
	}
}

func TestWAFCombinedPoliciesPreserveStrictestBodyLimit(t *testing.T) {
	cfg, e := wafFixture(t, true)
	if err := cfg.Update(func(c *config.Config) error {
		c.ProtectionPolicies = append(c.ProtectionPolicies, config.ProtectionPolicy{ID: "blocking", Name: "Blocking", Enabled: true, WAF: &config.WAFConfig{Enabled: true, BodyLimitKiB: 256}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://app.example/", strings.NewReader(strings.Repeat("x", 20<<10)))
	r.Header.Set("Content-Type", "text/plain")
	r.Header.Set("User-Agent", "Mozilla/5.0")
	r.Header.Set("Accept", "text/plain")
	w := httptest.NewRecorder()
	if e.InspectHTTP(w, r, "203.0.113.7", "rule", []string{"waf", "blocking"}) || w.Code != 413 {
		t.Fatal("less strict body limit replaced inherited limit", w.Code)
	}
}
