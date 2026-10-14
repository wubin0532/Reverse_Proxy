package acme

import (
	"andey-proxy/internal/config"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func importFixture(t *testing.T, change func(*x509.Certificate)) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), DNSNames: []string{"home.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(30 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if change != nil {
		change(leaf)
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})
}
func TestImportedPairValidation(t *testing.T) {
	cert, key := importFixture(t, nil)
	_, wrongKey := importFixture(t, nil)
	cases := []struct {
		name      string
		cert, key []byte
	}{{"mismatch", cert, wrongKey}, {"trailing garbage", append(append([]byte{}, cert...), []byte("junk")...), key}, {"two keys", cert, append(append([]byte{}, key...), key...)}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := validateImportedPair(c.cert, c.key, time.Now()); err == nil {
				t.Fatal("invalid pair accepted")
			}
		})
	}
	for _, name := range []string{"expired", "future", "ca", "cn only", "client only", "bad chain"} {
		t.Run(name, func(t *testing.T) {
			c, k := importFixture(t, func(leaf *x509.Certificate) {
				switch name {
				case "expired":
					leaf.NotAfter = time.Now().Add(-time.Minute)
				case "future":
					leaf.NotBefore = time.Now().Add(time.Hour)
				case "ca":
					leaf.IsCA = true
				case "cn only":
					leaf.DNSNames = nil
					leaf.Subject.CommonName = "home.example.com"
				case "client only":
					leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
				}
			})
			if name == "bad chain" {
				other, _ := importFixture(t, nil)
				c = append(c, other...)
			}
			if _, _, err := validateImportedPair(c, k, time.Now()); err == nil {
				t.Fatal("invalid certificate accepted")
			}
		})
	}
	if _, _, err := validateImportedPair(cert, key, time.Now()); err != nil {
		t.Fatal(err)
	}
}
func TestImportMetadataCoverageAndPersistenceRollback(t *testing.T) {
	cfg, m, r := newTestRouter(t)
	cert, key := importFixture(t, nil)
	input := importRequest{Name: "import", Enabled: true, CertPEM: string(cert), KeyPEM: string(key)}
	code, resp := doReq(t, r, "POST", "/api/certs/import", input)
	if code != 200 {
		t.Fatalf("import=%d %+v", code, resp)
	}
	data, _ := json.Marshal(resp.Data)
	var view certView
	json.Unmarshal(data, &view)
	if view.Source != "uploaded" || view.KeyType != "P256" || view.RemainingDays == nil || *view.RemainingDays < 28 || bytes.Contains(data, []byte("PRIVATE KEY")) {
		t.Fatalf("metadata: %s", data)
	}
	if err := m.ObtainAsync(view.ID); err == nil {
		t.Fatal("uploaded cert should not run ACME")
	}
	cfg.Lock()
	cfg.Sites = []config.Site{{ID: "site", CertID: view.ID}}
	cfg.Unlock()
	other, otherKey := importFixture(t, func(leaf *x509.Certificate) { leaf.DNSNames = []string{"other.example.com"} })
	input.ID = view.ID
	input.CertPEM = string(other)
	input.KeyPEM = string(otherKey)
	if code, _ := doReq(t, r, "POST", "/api/certs/import", input); code != 400 {
		t.Fatalf("bound SAN loss status=%d", code)
	}
	// Force the encrypted-config rename to fail after pair/state writes.
	confFile := filepath.Join(cfg.Dir(), "config.json")
	if err := os.Remove(confFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(confFile, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(confFile)
	newer, newKey := importFixture(t, nil)
	pair, _, err := validateImportedPair(newer, newKey, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	changed := view.CertConf
	changed.Name = "must rollback"
	if err = m.importCertificate(changed, true, pair, newer, newKey); err == nil {
		t.Fatal("persistence failure hidden")
	}
	cfg.RLock()
	name := cfg.Certs[0].Name
	cfg.RUnlock()
	if name != "import" {
		t.Fatal("config rollback failed")
	}
	savedCert, savedKey := m.certPath(&changed)
	c, _ := os.ReadFile(savedCert)
	k, _ := os.ReadFile(savedKey)
	if !bytes.Equal(c, cert) || !bytes.Equal(k, key) {
		t.Fatal("pair rollback failed")
	}
	if got, err := m.CertificateByID(view.ID); err != nil || !bytes.Equal(got.Certificate[0], pairDER(t, cert)) {
		t.Fatalf("cached bound pair changed on failed import: %v", err)
	}
}
func pairDER(t *testing.T, p []byte) []byte {
	t.Helper()
	block, _ := pem.Decode(p)
	return block.Bytes
}
func TestImportedPairConcurrentReadersAndKeyOnlyInvalidation(t *testing.T) {
	cfg := newTestConfig(t)
	m := NewManager(cfg)
	cert, key := importFixture(t, nil)
	pair, _, _ := validateImportedPair(cert, key, time.Now())
	c := config.CertConf{ID: "import", Name: "import", Enabled: true, Source: "uploaded", Domains: []string{"home.example.com"}}
	if err := m.importCertificate(c, false, pair, cert, key); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				got, err := m.CertificateByID(c.ID)
				if err != nil {
					t.Error(err)
					return
				}
				if err := got.Leaf.VerifyHostname("home.example.com"); err != nil {
					t.Error(err)
				}
				if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "home.example.com"}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	for i := 0; i < 4; i++ {
		cert, key = importFixture(t, nil)
		pair, _, _ = validateImportedPair(cert, key, time.Now())
		if err := m.importCertificate(c, true, pair, cert, key); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	_, badKey := importFixture(t, nil)
	_, keyFile := m.certPath(&c)
	if err := os.WriteFile(keyFile, badKey, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CertificateByID(c.ID); err == nil {
		t.Fatal("key-only change reused stale cache")
	}
}

func TestCertificateProviderReferenceMustExistAtCommit(t *testing.T) {
	cfg := newTestConfig(t)
	if err := validateCertProviderLocked(cfg, config.CertConf{ProviderID: "deleted"}); err == nil {
		t.Fatal("stale provider reference accepted")
	}
	if err := validateCertProviderLocked(cfg, config.CertConf{Source: "uploaded"}); err != nil {
		t.Fatal("PEM incorrectly requires provider")
	}
}
