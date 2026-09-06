package ddns

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"andey-proxy/internal/config"
)

func TestProviderTestRejectsMissingSavedID(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: cfg}
	req := httptest.NewRequest("POST", "/api/providers/test", bytes.NewBufferString(`{"id":"missing","domain":"www.example.com"}`))
	rec := httptest.NewRecorder()
	h.testProvider(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProviderTestRejectsTypeConfusion(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = []config.DNSProviderConf{{ID: "saved", Type: "aliyun", Key: "key", Secret: "secret"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: cfg}
	req := httptest.NewRequest("POST", "/api/providers/test", bytes.NewBufferString(`{"id":"saved","type":"cloudflare","domain":"www.example.com"}`))
	rec := httptest.NewRecorder()
	h.testProvider(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestValidateTaskWildcardDomain(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = []config.DNSProviderConf{{ID: "p1", Type: "cloudflare", Key: "k"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: cfg}

	base := func(domains ...string) *config.DDNSTask {
		return &config.DDNSTask{
			Name:       "t",
			Domains:    domains,
			ProviderID: "p1",
			IPType:     "ipv4",
			IPSource:   "api",
			APIURL:     "https://example.com/ip",
		}
	}

	for _, d := range []string{"*.example.com", "*.WUBIN.XYZ.", "www.example.com"} {
		if code, msg := h.validateTask(base(d)); code != 0 {
			t.Errorf("domain %q should pass, got code=%d msg=%s", d, code, msg)
		}
	}
	for _, d := range []string{"foo*bar.example.com", "*", "a.*.example.com", "*.*.example.com"} {
		if code, _ := h.validateTask(base(d)); code != 400 {
			t.Errorf("domain %q should be rejected with 400, got code=%d", d, code)
		}
	}
}
