package webproxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"andey-proxy/internal/config"
)

func TestHealthBackupRestoreUpdatesLiveProberAndRollsBackFailedSave(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer backend.Close()
	cfg, svc := newTestService(t)
	rule := config.SubRule{ID: "rule", Type: "reverse", Enabled: true, Backends: []string{backend.URL}}
	if err := cfg.Update(func(c *config.Config) error {
		c.Settings.AdminPassHash = "hash"
		c.Sites = []config.Site{{ID: "site", Name: "test", Listen: "127.0.0.1:0", Enabled: true, Rules: []config.SubRule{rule}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc.Start()
	code, _, _ := mustGet(t, httpClient(), "http://"+svc.ListenAddr("site")+"/", nil)
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	want := HealthCheckConf{Enabled: true, Type: "http", Path: "/healthy", IntervalSeconds: 17, TimeoutSeconds: 3, Rise: 2, Fall: 3}
	if err := svc.SetRuleHealth("site", rule, want); err != nil {
		t.Fatal(err)
	}
	plain, err := cfg.PlainJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteRuleHealth("site", rule); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Restore(plain); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := svc.HealthConf("rule"); got != want {
		t.Fatalf("restore retained stale config: %+v", got)
	}
	svc.mu.Lock()
	ss := svc.sites["site"]
	svc.mu.Unlock()
	ss.handlerMu.Lock()
	rh := ss.revHandler["rule"].(*reverseHandler)
	ss.handlerMu.Unlock()
	if !rh.activeCheck.Load() {
		t.Fatal("restored check did not start without new traffic")
	}
	if err := os.Mkdir(filepath.Join(cfg.Dir(), "config.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteRuleHealth("site", rule); err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := svc.HealthConf("rule"); got != want || !rh.activeCheck.Load() {
		t.Fatal("failed save changed configuration or live prober")
	}
}
