package acme

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func TestOperationHTTPClientKeepsBodyReadable(t *testing.T) {
	payload := bytes.Repeat([]byte("certificate-data"), 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := operationHTTPClient(ctx, server.Client()).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("response body truncated: %v", err)
	}
}

func TestOperationCancellationInterruptsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := operationHTTPClient(ctx, server.Client()).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(res.Body); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled body read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("response body did not respond to cancellation")
	}
}

func TestPropagationWaitRespondsToDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	ok, err := txtPropagationCheck(ctx, nil)("", "_acme-challenge.example.", "value", nil)
	if ok || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("propagation wait ignored deadline: ok=%t err=%v", ok, err)
	}
}

func TestObsoleteCertificateResultCannotReplaceFilesOrState(t *testing.T) {
	for _, change := range []string{"domains", "credentials", "disabled", "deleted", "canceled"} {
		t.Run(change, func(t *testing.T) {
			cfg := newTestConfig(t)
			cert := writeCertFiles(t, cfg, "c", []string{"old.example"}, time.Now().Add(90*24*time.Hour))
			provider := config.DNSProviderConf{ID: "p", Type: "cloudflare", Secret: "fixture-token"}
			cert.ProviderID = provider.ID
			cfg.Certs = []config.CertConf{cert}
			cfg.Providers = []config.DNSProviderConf{provider}
			manager := NewManager(cfg)
			defer manager.Stop()
			crtPath, _ := manager.certPath(&cert)
			original, err := os.ReadFile(crtPath)
			if err != nil {
				t.Fatal(err)
			}
			originalState := cfg.State().Cert(cert.ID)
			if err := cfg.Update(func(c *config.Config) error {
				switch change {
				case "domains":
					c.Certs[0].Domains = []string{"new.example"}
				case "credentials":
					c.Providers[0].Secret = "changed-token"
				case "disabled":
					c.Certs[0].Enabled = false
				case "deleted":
					c.Certs = nil
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if change == "canceled" {
				cancel()
			}
			newCert, newKey := genSelfSigned(t, cert.Domains, time.Now().Add(180*24*time.Hour))
			_, err = manager.commitCertificate(ctx, cert, provider, newCert, newKey)
			if !errors.Is(err, errRequestChanged) && !errors.Is(err, context.Canceled) {
				t.Fatalf("obsolete result was accepted: %v", err)
			}
			got, err := os.ReadFile(crtPath)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("obsolete result replaced the certificate")
			}
			if gotState := cfg.State().Cert(cert.ID); gotState != originalState {
				t.Fatal("obsolete result replaced certificate state")
			}
		})
	}
}

func TestCertificateCoverageIncludesEveryRequestedDomain(t *testing.T) {
	crt, key := genSelfSigned(t, []string{"*.example.com", "example.com"}, time.Now().Add(90*24*time.Hour))
	pair, err := tls.X509KeyPair(crt, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		domains []string
		want    bool
	}{
		{[]string{"*.example.com", "example.com"}, true},
		{[]string{"www.example.com"}, true},
		{[]string{"a.b.example.com"}, false},
		{[]string{"example.com", "other.example"}, false},
		{[]string{"*.other.example"}, false},
	} {
		if got := coversDomains(&pair, tt.domains); got != tt.want {
			t.Errorf("coverage(%v)=%t", tt.domains, got)
		}
	}
}

func TestAsyncObtainReservationsStopWithManager(t *testing.T) {
	cfg := newTestConfig(t)
	manager := NewManager(cfg)
	if err := manager.ObtainAsync("nonexistent"); err != nil {
		t.Fatal(err)
	}
	manager.Stop()
	if manager.Obtaining("nonexistent") {
		t.Fatal("Stop returned before the reserved task finished")
	}
	if err := manager.ObtainAsync("nonexistent"); err == nil {
		t.Fatal("accepted a task after Stop")
	}
	manager.Start()
	manager.Reload()
	manager.Stop()
}
