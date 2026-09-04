package acme

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"luckyx/internal/config"
)

// genSelfSigned 生成一张自签证书并写入指定路径，返回证书 PEM。
func genSelfSigned(t *testing.T, dnsNames []string, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

// writeCertFiles 把证书写入配置目录的 certs/ 下并返回填充好路径的 CertConf。
func writeCertFiles(t *testing.T, cfg *config.Config, id string, dnsNames []string, notAfter time.Time) config.CertConf {
	t.Helper()
	certPEM, keyPEM := genSelfSigned(t, dnsNames, notAfter)
	dir := filepath.Join(cfg.Dir(), "certs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".crt"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return config.CertConf{
		ID:       id,
		Name:     id,
		Enabled:  true,
		Domains:  dnsNames,
		CertFile: filepath.Join("certs", id+".crt"),
		KeyFile:  filepath.Join("certs", id+".key"),
		NotAfter: notAfter.UTC().Format(time.RFC3339),
	}
}

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "www.example.com", false},
		{"*.example.com", "www.example.com", true},
		{"*.example.com", "example.com", false},     // 泛域名不匹配裸域
		{"*.example.com", "a.b.example.com", false}, // 只匹配一级子域
		{"*.example.com", "other.com", false},
		{"Example.COM", "example.com", true},        // 大小写不敏感
		{"*.example.com.", "www.example.com", true}, // 容忍结尾点
	}
	for _, c := range cases {
		if got := matchDomain(c.pattern, c.name); got != c.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestGetCertificateSNIPriority(t *testing.T) {
	cfg := newTestConfig(t)
	m := NewManager(cfg)

	wildcard := writeCertFiles(t, cfg, "wild", []string{"*.example.com"}, time.Now().Add(90*24*time.Hour))
	exact := writeCertFiles(t, cfg, "exact", []string{"www.example.com"}, time.Now().Add(90*24*time.Hour))
	cfg.Lock()
	cfg.Certs = append(cfg.Certs, wildcard, exact)
	cfg.Unlock()

	// 精确匹配优先于泛域名
	cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "www.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if cert.Leaf == nil {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		cert.Leaf = leaf
	}
	if cert.Leaf.Subject.CommonName != "www.example.com" {
		t.Fatalf("期望精确匹配证书，得到 %s", cert.Leaf.Subject.CommonName)
	}

	// 缓存命中：两次调用应返回同一指针
	cert2, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "www.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if cert != cert2 {
		t.Fatal("缓存未命中，期望返回同一 *tls.Certificate")
	}

	// 泛域名回退
	cert3, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "mail.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	leaf3, err := x509.ParseCertificate(cert3.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf3.Subject.CommonName != "*.example.com" {
		t.Fatalf("期望泛域名证书，得到 %s", leaf3.Subject.CommonName)
	}

	// 无匹配返回 ErrNotExist
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "nope.other.com"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("期望 os.ErrNotExist，得到 %v", err)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("空 SNI 期望 os.ErrNotExist，得到 %v", err)
	}
}

func TestGetCertificateReloadOnMtime(t *testing.T) {
	cfg := newTestConfig(t)
	m := NewManager(cfg)
	c := writeCertFiles(t, cfg, "c1", []string{"a.example.com"}, time.Now().Add(90*24*time.Hour))
	cfg.Lock()
	cfg.Certs = append(cfg.Certs, c)
	cfg.Unlock()

	first, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// 重写证书文件并推进 mtime，应触发重载
	certPEM, keyPEM := genSelfSigned(t, []string{"a.example.com"}, time.Now().Add(180*24*time.Hour))
	certFile, keyFile := m.certPath(&c)
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	newTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(certFile, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	second, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("mtime 变化后未重载证书")
	}
}

func TestParseNotAfter(t *testing.T) {
	want := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
	certPEM, _ := genSelfSigned(t, []string{"x.example.com"}, want)
	got, err := parseNotAfter(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	// x509 时间精度到秒
	if !got.Equal(want) {
		t.Fatalf("parseNotAfter = %s, want %s", got, want)
	}
	if _, err := parseNotAfter([]byte("not a pem")); err == nil {
		t.Fatal("非法 PEM 应报错")
	}
}

func TestNeedRenew(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		cert config.CertConf
		want bool
	}{
		{"NotAfter 缺失需补申请", config.CertConf{}, true},
		{"NotAfter 无法解析需重签", config.CertConf{NotAfter: "bad"}, true},
		{"距到期 60 天 > 默认 30 天不续", config.CertConf{NotAfter: now.Add(60 * 24 * time.Hour).Format(time.RFC3339)}, false},
		{"距到期 10 天 < 默认 30 天续签", config.CertConf{NotAfter: now.Add(10 * 24 * time.Hour).Format(time.RFC3339)}, true},
		{"已过期需续签", config.CertConf{NotAfter: now.Add(-time.Hour).Format(time.RFC3339)}, true},
		{"自定义 RenewDays=90 时 60 天需续", config.CertConf{NotAfter: now.Add(60 * 24 * time.Hour).Format(time.RFC3339), RenewDays: 90}, true},
		{"自定义 RenewDays=7 时 10 天不续", config.CertConf{NotAfter: now.Add(10 * 24 * time.Hour).Format(time.RFC3339), RenewDays: 7}, false},
	}
	for _, c := range cases {
		if got := needRenew(&c.cert, now); got != c.want {
			t.Errorf("%s: needRenew = %v, want %v", c.name, got, c.want)
		}
	}
}
