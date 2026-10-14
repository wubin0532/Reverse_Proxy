package acme

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func TestEABRegistrationAndSecretRedaction(t *testing.T) {
	raw := []byte("0123456789abcdefghijklmnopqrstuv")
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	var registered atomic.Bool
	var ca *httptest.Server
	ca = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString([]byte("fresh-test-nonce-1234567890")))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/directory":
			fmt.Fprintf(w, `{"newNonce":%q,"newAccount":%q,"newOrder":%q,"revokeCert":%q,"keyChange":%q,"meta":{"externalAccountRequired":true}}`, ca.URL+"/nonce", ca.URL+"/account", ca.URL+"/order", ca.URL+"/revoke", ca.URL+"/change")
		case "/nonce":
			w.WriteHeader(200)
		case "/account":
			var jws struct{ Payload string }
			if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
				t.Error(err)
			}
			payload, err := base64.RawURLEncoding.DecodeString(jws.Payload)
			if err != nil {
				t.Error(err)
			}
			var account struct {
				EAB struct{ Protected, Payload, Signature string } `json:"externalAccountBinding"`
			}
			if err := json.Unmarshal(payload, &account); err != nil {
				t.Error(err)
			}
			sum := hmac.New(sha256.New, raw)
			sum.Write([]byte(account.EAB.Protected + "." + account.EAB.Payload))
			want := base64.RawURLEncoding.EncodeToString(sum.Sum(nil))
			if account.EAB.Payload == "" || account.EAB.Signature != want {
				t.Error("EAB binding signature not present or invalid")
			} else {
				registered.Store(true)
			}
			w.Header().Set("Location", ca.URL+"/account/1")
			w.WriteHeader(201)
			fmt.Fprint(w, `{"status":"valid"}`)
		case "/order":
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"type": "urn:ietf:params:acme:error:rejectedIdentifier", "detail": "fixture rejection " + encoded})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ca.Close()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	cert := config.CertConf{ID: "cert", Name: "EAB", Enabled: true, Domains: []string{"home.example.com"}, ProviderID: "cf", CADirURL: ca.URL + "/directory", CATrustPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate().Raw})), EABEnabled: true, EABKeyID: "fixture-id", EABHMAC: encoded, DNSResolvers: []string{"127.0.0.1:5353"}, PropagationTimeout: 30, PropagationInterval: 2}
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = []config.DNSProviderConf{{ID: "cf", Type: "cloudflare", Key: "fixture-token"}}
		c.Certs = []config.CertConf{cert}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(cfg)
	defer manager.Stop()
	err = manager.Obtain(context.Background(), cert.ID)
	if err == nil || !registered.Load() {
		t.Fatal("mock order was not rejected after EAB registration", err)
	}
	if strings.Contains(err.Error(), encoded) || strings.Contains(cfg.State().Cert(cert.ID).LastError, encoded) {
		t.Fatal("EAB secret leaked into error/state")
	}
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, manager)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/certs", nil))
	if strings.Contains(w.Body.String(), encoded) || !strings.Contains(w.Body.String(), `"eabConfigured":true`) {
		t.Fatal("EAB read contract", w.Body.String())
	}
	cert.EABHMAC = ""
	b, _ := json.Marshal(cert)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("PUT", "/api/certs/cert", bytes.NewReader(b)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cfg.RLock()
	kept := cfg.Certs[0].EABHMAC
	cfg.RUnlock()
	if kept != encoded {
		t.Fatal("blank update lost EAB secret")
	}
}
func TestCertificateExportAtomicAndRooted(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cert, key := importFixture(t, nil)
	base := filepath.Join("exports", "cert", "nginx")
	for i := 0; i < 4; i++ {
		if err := exportPair(root, base, cert, key); err != nil {
			t.Fatal(err)
		}
	}
	target, err := root.Readlink(filepath.Join(base, "current"))
	if err != nil {
		t.Fatal(err)
	}
	certRead, _ := root.ReadFile(filepath.Join(base, target, "fullchain.pem"))
	keyRead, _ := root.ReadFile(filepath.Join(base, target, "privkey.pem"))
	if _, err := tls.X509KeyPair(certRead, keyRead); err != nil {
		t.Fatal(err)
	}
	bundle, err := root.ReadFile(filepath.Join(base, "current", "bundle.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(bundle, bundle); err != nil {
		t.Fatal("combined bundle invalid", err)
	}
	bundleStat, err := root.Stat(filepath.Join(base, target, "bundle.pem"))
	if err != nil || bundleStat.Mode().Perm() != 0o600 {
		t.Fatal("bundle permissions", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, base, "versions"))
	if err != nil || len(entries) != 2 {
		t.Fatal("retained generation count", len(entries), err)
	}
	stat, _ := root.Stat(filepath.Join(base, target, "privkey.pem"))
	if stat.Mode().Perm() != 0o600 {
		t.Fatal("private key mode", stat.Mode())
	}
	before, _ := root.Readlink(filepath.Join(base, "current"))
	if exportPair(root, base, cert, []byte("invalid")) == nil {
		t.Fatal("mismatched key exported")
	}
	after, _ := root.Readlink(filepath.Join(base, "current"))
	if before != after {
		t.Fatal("failed export replaced current")
	}
	external := t.TempDir()
	root.MkdirAll("attack", 0o700)
	if err := root.Symlink(external, "attack/out"); err != nil {
		t.Fatal(err)
	}
	if exportPair(root, "attack/out/escaped", cert, key) == nil {
		t.Fatal("export escaped rooted directory")
	}
	entries, _ = os.ReadDir(external)
	if len(entries) != 0 {
		t.Fatal("private material written outside root")
	}
}
func TestMappingRevisionAndValidation(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	cfg.Update(func(c *config.Config) error {
		c.Certs = []config.CertConf{{ID: "c", Name: "import", Source: "uploaded", Domains: []string{"home.example.com"}}}
		return nil
	})
	manager := NewManager(cfg)
	defer manager.Stop()
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, manager)
	body := map[string]any{"revision": mappingRevision(nil), "mappings": []config.CertificateMapping{{Name: "nginx"}}}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("PUT", "/api/certs/c/mappings", bytes.NewReader(b)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("PUT", "/api/certs/c/mappings", bytes.NewReader(b)))
	if w.Code != 409 {
		t.Fatal("stale mapping update accepted", w.Code)
	}
	body["revision"] = mappingRevision([]config.CertificateMapping{{Name: "nginx"}})
	body["mappings"] = []config.CertificateMapping{{Name: "../../etc", ReloadService: "sh"}}
	b, _ = json.Marshal(body)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("PUT", "/api/certs/c/mappings", bytes.NewReader(b)))
	if w.Code != 400 {
		t.Fatal("unsafe mapping accepted", w.Code)
	}
}
