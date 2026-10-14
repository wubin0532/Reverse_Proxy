package webproxy

import (
	"andey-proxy/internal/acme"
	"andey-proxy/internal/config"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/go-chi/chi/v5"
	"math/big"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestImportReplacementDuringVerifiedSNIAndNoSNIHandshakes(t *testing.T) {
	cfg, svc := newTestService(t)
	manager := acme.NewManager(cfg)
	svc.BoundCertGetter = manager.CertificateByID
	router := chi.NewRouter()
	acme.RegisterRoutes(router, cfg, manager)
	roots := x509.NewCertPool()
	type material struct{ cert, key string }
	var pairs []material
	for i := 0; i < 2; i++ {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 1)), DNSNames: []string{"home.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := x509.ParseCertificate(der)
		roots.AddCert(parsed)
		keyDER, _ := x509.MarshalECPrivateKey(key)
		pairs = append(pairs, material{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))})
	}
	id := ""
	publish := func(p material) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": id, "name": "fixture", "enabled": true, "certPEM": p.cert, "keyPEM": p.key})
		res := callWebAPI(t, router, "POST", "/api/certs/import", string(body))
		if res.Code != 200 {
			t.Fatalf("import failed: %s", res.Body.String())
		}
		var view struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		json.Unmarshal(res.Body.Bytes(), &view)
		id = view.Data.ID
	}
	publish(pairs[0])
	site := config.Site{Name: "fixture", CertID: id}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })}
	done := make(chan struct{})
	go func() {
		server.Serve(tls.NewListener(ln, svc.tlsConfig(func() config.Site { return site })))
		close(done)
	}()
	defer func() { server.Close(); <-done }()
	serverURL := "https://" + ln.Addr().String()

	var wg sync.WaitGroup
	for _, sni := range []bool{true, false} {
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(sni bool) {
				defer wg.Done()
				tlsConf := &tls.Config{RootCAs: roots, ServerName: "home.example.com", MinVersion: tls.VersionTLS12}
				if !sni {
					tlsConf.ServerName = ""
					tlsConf.InsecureSkipVerify = true
					tlsConf.VerifyConnection = func(state tls.ConnectionState) error {
						_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, DNSName: "home.example.com"})
						return err
					}
				}
				client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConf, DisableKeepAlives: true}, Timeout: 3 * time.Second}
				defer client.CloseIdleConnections()
				for j := 0; j < 20; j++ {
					response, err := client.Get(serverURL)
					if err != nil {
						t.Error(err)
						return
					}
					response.Body.Close()
					if response.StatusCode != 200 {
						t.Errorf("handshake request status=%d", response.StatusCode)
					}
				}
			}(sni)
		}
	}
	for i := 0; i < 8; i++ {
		publish(pairs[i%2])
	}
	wg.Wait()
	final, err := manager.CertificateByID(id)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(pairs[1].cert))
	if !bytes.Equal(final.Certificate[0], block.Bytes) {
		t.Fatal("last replacement not published")
	}
}
