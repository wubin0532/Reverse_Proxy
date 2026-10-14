package webproxy

import (
	"andey-proxy/internal/config"
	"andey-proxy/internal/guard"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuleHealthAtomicSaveAndStaleRevision(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{ID: "s", Name: "site", Listen: freeRuleAPIAddr(t), Rules: []config.SubRule{{ID: "r", Name: "old", Type: "reverse", FrontendPath: "/", Backends: []string{"http://127.0.0.1:12345"}}}})
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, svc)
	bad := `{"name":"bad","type":"reverse","frontendPath":"/","backends":["http://127.0.0.1:12345"],"expectedRevision":0,"healthCheck":{"enabled":true,"intervalSeconds":1}}`
	if res := callWebAPI(t, router, "PUT", "/api/sites/s/rules/r", bad); res.Code != 400 {
		t.Fatalf("invalid health = %d %s", res.Code, res.Body.String())
	}
	cfg.RLock()
	name := cfg.Sites[0].Rules[0].Name
	cfg.RUnlock()
	if name != "old" || svc.HealthConf("r").Enabled {
		t.Fatal("partial invalid save")
	}
	good := `{"name":"good","type":"reverse","frontendPath":"/","backends":["http://127.0.0.1:12345"],"expectedRevision":0,"healthCheck":{"enabled":true,"intervalSeconds":5,"timeoutSeconds":2}}`
	if res := callWebAPI(t, router, "PUT", "/api/sites/s/rules/r", good); res.Code != 200 {
		t.Fatalf("atomic save=%d %s", res.Code, res.Body.String())
	}
	if res := callWebAPI(t, router, "PUT", "/api/sites/s/rules/r", good); res.Code != 409 {
		t.Fatalf("stale save=%d", res.Code)
	}
	disk := loadHealthTestConfig(t, cfg.Dir())
	if disk.Sites[0].Rules[0].Name != "good" || !disk.RuleHealthChecks["r"].Enabled || disk.Sites[0].Rules[0].Revision != 1 {
		t.Fatal("rule and health not committed together")
	}
}
func TestDefaultResponseCannotBypassSitePolicyOrSpoofClient(t *testing.T) {
	cfg, svc := newTestService(t)
	cfg.Lock()
	cfg.ProtectionPolicies = []config.ProtectionPolicy{{ID: "p", Name: "deny local", Enabled: true, IPListMode: "blacklist", IPList: []string{"127.0.0.1"}}}
	cfg.Unlock()
	svc.Protection = guard.NewEngine(cfg)
	addSite(cfg, config.Site{ID: "s", Name: "site", Enabled: true, Listen: freeRuleAPIAddr(t), PolicyID: "p", DefaultResponse: &config.DefaultResponse{StatusCode: 418, Body: "default"}})
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	url := "http://" + svc.ListenAddr("s") + "/unmatched"
	code, _, _ := mustGet(t, httpClient(), url, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "198.51.100.9")
		r.Header.Set("CF-Connecting-IP", "198.51.100.9")
	})
	if code != 403 || svc.Protection.Summary().Rejected != 1 {
		t.Fatalf("unmatched protection code=%d count=%d", code, svc.Protection.Summary().Rejected)
	}
	cfg.Lock()
	cfg.ProtectionPolicies[0].Enabled = false
	cfg.Unlock()
	if err := svc.Protection.Reload(); err != nil {
		t.Fatal(err)
	}
	code, _, body := mustGet(t, httpClient(), url, nil)
	if code != 418 || body != "default" {
		t.Fatalf("default=%d %q", code, body)
	}
	req := httptest.NewRequest("HEAD", url, nil)
	ss := svc.sites["s"]
	rec := httptest.NewRecorder()
	(&siteHandler{ss: ss}).ServeHTTP(rec, req)
	if rec.Code != 418 || rec.Body.Len() != 0 {
		t.Fatal("HEAD default contains body")
	}
}

func TestSiteCertificateReferenceMustExistAtCommit(t *testing.T) {
	cfg, _ := newTestService(t)
	cfg.RLock()
	err := validateReferences(cfg, config.Site{CertID: "deleted"})
	cfg.RUnlock()
	if err == nil {
		t.Fatal("stale certificate reference accepted")
	}
}
