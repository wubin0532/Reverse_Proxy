package webproxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"andey-proxy/internal/config"
)

func TestPinnedCertificateFallbackRejectsWrongHostname(t *testing.T) {
	cfg, svc := newTestService(t)
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	pair := fixture.TLS.Certificates[0]
	dir := filepath.Join(cfg.Dir(), "certs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"pinned.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}),
		"pinned.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	get := svc.tlsConfig(func() config.Site { return config.Site{Name: "pinned", CertID: "pinned"} }).GetCertificate
	got, err := get(&tls.ClientHelloInfo{ServerName: "new.example"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got.Certificate[0], pair.Certificate[0]) {
		t.Fatal("fallback served the old hostname certificate")
	}
	// Matching SNI and clients without SNI retain the explicitly pinned fallback.
	for _, name := range []string{fixture.Certificate().DNSNames[0], ""} {
		got, err := get(&tls.ClientHelloInfo{ServerName: name})
		if err != nil || !bytes.Equal(got.Certificate[0], pair.Certificate[0]) {
			t.Fatalf("valid pinned fallback failed: %v", err)
		}
	}
}
