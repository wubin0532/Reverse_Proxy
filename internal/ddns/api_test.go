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
