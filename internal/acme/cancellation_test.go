package acme

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

// Review probe: local CA only; no real certificate issuance or DNS changes.
func TestCanceledObtainStopsHTTP(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		http.Error(w, "local review CA", http.StatusBadRequest)
	}))
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "local-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", caPath)
	cfg := newTestConfig(t)
	cfg.Providers = []config.DNSProviderConf{{ID: "cf", Type: "cloudflare", Secret: "local-placeholder"}}
	cfg.Certs = []config.CertConf{{ID: "c", Name: "c", Enabled: true, Domains: []string{"old.example"}, ProviderID: "cf", CADirURL: server.URL}}
	manager := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.Obtain(ctx, "c") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("local CA not reached")
	}
	cancel()
	canceled := false
	select {
	case <-done:
		canceled = true
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if !canceled {
		<-done
	}
	manager.Stop()
	if !canceled {
		t.Fatal("cancellation did not interrupt the in-flight ACME HTTP request")
	}
}

func TestDomainEditDoesNotServeOldCertificate(t *testing.T) {
	cfg, manager, router := newTestRouter(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "local review CA", 400) }))
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "local-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", caPath)
	cert := writeCertFiles(t, cfg, "changed", []string{"old.example"}, time.Now().Add(90*24*time.Hour))
	cert.ProviderID = "aliyun-1"
	cert.CADirURL = server.URL
	cfg.Certs = []config.CertConf{cert}
	cert.Domains = []string{"new.example"}
	code, _ := doReq(t, router, http.MethodPut, "/api/certs/changed", cert)
	if code != 200 {
		t.Fatalf("update failed: %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for cfg.State().Cert(cert.ID).LastError == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	manager.Stop()
	if cfg.State().Cert(cert.ID).LastError == "" {
		t.Fatal("failed reissue did not reach the error state")
	}
	pair, err := manager.GetCertificate(&tls.ClientHelloInfo{ServerName: "new.example"})
	if err != nil {
		return
	}
	if err := pair.Leaf.VerifyHostname("new.example"); err != nil {
		t.Fatalf("new SNI receives old certificate: %v", err)
	}
}

func TestStopCancelsBackgroundObtain(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		http.Error(w, "local review CA", 400)
	}))
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "local-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", caPath)
	cfg := newTestConfig(t)
	cfg.Providers = []config.DNSProviderConf{{ID: "cf", Type: "cloudflare", Secret: "local-placeholder"}}
	cfg.Certs = []config.CertConf{{ID: "c", Name: "c", Enabled: true, Domains: []string{"old.example"}, ProviderID: "cf", CADirURL: server.URL}}
	manager := NewManager(cfg)
	manager.Reload()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		manager.Stop()
		t.Fatal("local CA not reached")
	}
	stopped := make(chan struct{})
	go func() { manager.Stop(); close(stopped) }()
	canceled := false
	select {
	case <-stopped:
		canceled = true
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if !canceled {
		<-stopped
		t.Fatal("Stop cannot cancel the background ACME request and remains blocked")
	}
}
