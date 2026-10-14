package webproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"andey-proxy/internal/config"
	"andey-proxy/internal/forward"
)

func TestAdvancedCORSRewriteHeadersAndAccessLog(t *testing.T) {
	var requestURI atomic.Value
	requestURI.Store("")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI.Store(r.RequestURI)
		w.Header().Set("X-Remove", "backend")
		w.Header().Set("Access-Control-Allow-Origin", "https://evil.example")
		io.WriteString(w, "backend")
	}))
	defer backend.Close()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	rule := config.SubRule{ID: "rule", Name: "Rule", Type: "reverse", Enabled: true, FrontendPath: "/api", Backends: []string{backend.URL}, PathRewritePrefix: "/v2", ResponseHeaders: map[string]string{"X-Set": "value", "X-Remove": ""}, DisableAccessLog: true, BasicAuth: true, AuthUser: "user", AuthPass: "secret", CORS: &config.CORSConfig{Enabled: true, Origins: []string{"https://app.example"}, Methods: []string{"GET"}, Headers: []string{"X-Test"}, Credentials: true, MaxAge: 600}}
	if err := cfg.Update(func(c *config.Config) error {
		c.Sites = []config.Site{{ID: "s", Name: "Site", Enabled: true, Listen: "127.0.0.1:0", Rules: []config.SubRule{rule}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfg, nil)
	svc.Start()
	defer svc.Stop()
	u := "http://" + svc.ListenAddr("s")
	req, _ := http.NewRequest("OPTIONS", u+"/api/x", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "X-Test")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || requestURI.Load() != "" {
		t.Fatalf("preflight forwarded or failed: %d %s", res.StatusCode, requestURI.Load())
	}
	req, _ = http.NewRequest("GET", u+"/api/a%2Fb?q=1", nil)
	req.Header.Set("Origin", "https://app.example")
	req.SetBasicAuth("user", "secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || requestURI.Load() != "/v2/a%2Fb?q=1" {
		t.Fatal(res.StatusCode, requestURI.Load())
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "https://app.example" || res.Header.Get("X-Remove") != "" || res.Header.Get("X-Set") != "value" {
		t.Fatal(res.Header)
	}
	if len(svc.Logs("s")) != 0 {
		t.Fatal("access log disabled but ordinary request logged")
	}
	req, _ = http.NewRequest("GET", u+"/api/x", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.SetBasicAuth("user", "secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("untrusted origin allowed", res.StatusCode)
	}
	// No credentials on the actual request must still fail after successful preflight.
	req, _ = http.NewRequest("GET", u+"/api/x", nil)
	req.Header.Set("Origin", "https://app.example")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("preflight disabled real authentication")
	}
}
func TestAdvancedRejectsProtocolHeadersAndUnsafeCORS(t *testing.T) {
	for _, rule := range []config.SubRule{
		{ResponseHeaders: map[string]string{"Content-Length": "123"}},
		{ResponseHeaders: map[string]string{"Set-Cookie": "admin=unsafe"}},
		{ResponseHeaders: map[string]string{"X-Test": "ok\r\nInjected: bad"}},
		{PathRewritePrefix: "/../private"},
		{CORS: &config.CORSConfig{Enabled: true, Origins: []string{"*"}, Methods: []string{"GET"}, Credentials: true}},
		{CORS: &config.CORSConfig{Enabled: true, Origins: []string{"https://app.example/path"}, Methods: []string{"GET"}}},
	} {
		if rule.ValidateAdvanced() == nil {
			t.Fatalf("unsafe configuration accepted: %+v", rule)
		}
	}
}
func TestAdvancedCustomProxyError(t *testing.T) {
	h, err := newReverseHandler(config.SubRule{ID: "r", Backends: []string{"http://127.0.0.1:1"}, ProxyErrorBody: "service unavailable"}, forward.NewRingLog(10))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "http://example/", strings.NewReader("body")))
	if w.Code != 502 || w.Body.String() != "service unavailable" {
		t.Fatal(w.Code, w.Body.String())
	}
}
