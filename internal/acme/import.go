package acme

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
)

const maxPEMSize = 256 << 10

// PEM material is accepted only by this write endpoint; never part of CertConf.
type importRequest struct {
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Domains []string `json:"domains,omitempty"`
	CertPEM string   `json:"certPEM"`
	KeyPEM  string   `json:"keyPEM"`
}

func validateImportedPair(certPEM, keyPEM []byte, now time.Time) (*tls.Certificate, *x509.Certificate, error) {
	if len(certPEM) == 0 || len(keyPEM) == 0 || len(certPEM) > maxPEMSize || len(keyPEM) > maxPEMSize {
		return nil, nil, errors.New("证书和私钥不能为空，且各自不能超过 256 KiB")
	}
	rest := bytes.TrimSpace(certPEM)
	count := 0
	var chain []*x509.Certificate
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, nil, errors.New("证书包含非 PEM 内容")
		}
		block, tail := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, nil, errors.New("证书必须为 PEM 证书链，不可包含私钥或其他内容")
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, errors.New("证书内容无法解析")
		}
		chain = append(chain, parsed)
		count++
		rest = bytes.TrimSpace(tail)
	}
	if count == 0 {
		return nil, nil, errors.New("证书 PEM 格式不正确")
	}
	keyData := bytes.TrimSpace(keyPEM)
	if !bytes.HasPrefix(keyData, []byte("-----BEGIN ")) {
		return nil, nil, errors.New("私钥包含非 PEM 内容")
	}
	block, tail := pem.Decode(keyData)
	if block == nil || len(bytes.TrimSpace(tail)) != 0 || len(block.Headers) != 0 ||
		(block.Type != "PRIVATE KEY" && block.Type != "RSA PRIVATE KEY" && block.Type != "EC PRIVATE KEY") {
		return nil, nil, errors.New("私钥必须为未加密的单个 PEM 私钥")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, errors.New("证书与私钥不匹配或私钥无法解析")
	}
	leaf, err := certificateLeaf(&pair)
	if err != nil {
		return nil, nil, errors.New("证书内容无法解析")
	}
	if leaf.IsCA || len(leaf.DNSNames) == 0 {
		return nil, nil, errors.New("必须导入包含 DNS SAN 的服务器证书，不接受 CA 证书或仅 CN 证书")
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, nil, errors.New("证书尚未生效或已经过期")
	}
	if leaf.KeyUsage != 0 && leaf.KeyUsage&(x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment) == 0 {
		return nil, nil, errors.New("证书密钥用途不支持 TLS 服务器")
	}
	if len(leaf.ExtKeyUsage) > 0 || len(leaf.UnknownExtKeyUsage) > 0 {
		server := false
		for _, use := range leaf.ExtKeyUsage {
			if use == x509.ExtKeyUsageServerAuth || use == x509.ExtKeyUsageAny {
				server = true
			}
		}
		if !server {
			return nil, nil, errors.New("证书扩展用途不支持 TLS 服务器")
		}
	}
	for i := 1; i < len(chain); i++ {
		if now.Before(chain[i].NotBefore) || !now.Before(chain[i].NotAfter) || chain[i-1].CheckSignatureFrom(chain[i]) != nil {
			return nil, nil, errors.New("证书链顺序、签名或有效期不正确")
		}
	}
	pair.Leaf = leaf
	return &pair, leaf, nil
}

func importedKeyType(pair *tls.Certificate) string {
	switch key := pair.PrivateKey.(type) {
	case *ecdsa.PrivateKey:
		return fmt.Sprintf("P%d", key.Curve.Params().BitSize)
	case *rsa.PrivateKey:
		return fmt.Sprintf("RSA%d", key.N.BitLen())
	default:
		return "other"
	}
}

func (h *handler) importCert(w http.ResponseWriter, r *http.Request) {
	var input importRequest
	if err := api.DecodeBody(r, &input); err != nil {
		api.Fail(w, 400, "请求格式错误或内容超过大小限制")
		return
	}
	pair, leaf, err := validateImportedPair([]byte(input.CertPEM), []byte(input.KeyPEM), time.Now())
	if err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
	domains := input.Domains
	if len(domains) == 0 {
		domains = append([]string(nil), leaf.DNSNames...)
	}
	c := config.CertConf{ID: input.ID, Name: input.Name, Enabled: input.Enabled, Domains: domains, Source: "uploaded", KeyType: importedKeyType(pair)}
	if code, msg := h.validateCert(&c); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	if !coversDomains(pair, c.Domains) {
		api.Fail(w, 400, "证书 SAN 未覆盖配置的全部域名")
		return
	}
	replacing := c.ID != ""
	if c.ID == "" {
		c.ID = ids.New()
	}
	if err := h.m.importCertificate(c, replacing, pair, []byte(input.CertPEM), []byte(input.KeyPEM)); err != nil {
		if errors.Is(err, errCertNotFound) {
			api.Fail(w, 404, "证书不存在")
			return
		}
		if errors.Is(err, errCertCoverage) {
			api.Fail(w, 400, "替换证书未覆盖当前绑定站点使用的域名")
			return
		}
		api.Fail(w, 500, "导入保存失败，原证书保留")
		return
	}
	h.m.autoSyncMappings(c.ID)
	api.OK(w, h.viewOf(c, time.Now()))
}

var errCertCoverage = errors.New("import does not cover existing bound domains")

// Config.Update owns cfg lock before pairMu; TLS readers remain excluded until
// encrypted config persistence succeeds or the previous files are restored.
func (m *Manager) importCertificate(c config.CertConf, replacing bool, pair *tls.Certificate, certPEM, keyPEM []byte) error {
	locked, wrote, stateChanged := false, false, false
	var oldState config.CertState
	var oldCert, oldKey []byte
	var oldCertExists, oldKeyExists bool
	certFile, keyFile := m.certPath(&c)
	defer func() {
		if locked {
			m.pairMu.Unlock()
		}
	}()
	err := m.cfg.Update(func(cfg *config.Config) error {
		index := -1
		for i, current := range cfg.Certs {
			if current.ID == c.ID {
				index = i
				break
			}
		}
		if replacing && index >= 0 {
			c.Mappings = append([]config.CertificateMapping(nil), cfg.Certs[index].Mappings...)
		}
		if replacing && index < 0 {
			return errCertNotFound
		}
		for _, site := range cfg.Sites {
			if site.CertID != c.ID {
				continue
			}
			// A replacement must retain all existing declared domains on a bound cert.
			if index >= 0 && !coversDomains(pair, cfg.Certs[index].Domains) {
				return errCertCoverage
			}
		}
		m.pairMu.Lock()
		locked = true
		oldState = m.cfg.State().Cert(c.ID)
		var err error
		oldCert, err = os.ReadFile(certFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		oldCertExists = err == nil
		oldKey, err = os.ReadFile(keyFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		oldKeyExists = err == nil
		if err := os.MkdirAll(m.certsDir(), 0o700); err != nil {
			return err
		}
		if err := writeCertificatePair(certFile, keyFile, certPEM, keyPEM); err != nil {
			return err
		}
		wrote = true
		stateChanged = true
		m.cfg.State().Update(func(s *config.State) {
			s.Certs[c.ID] = config.CertState{CertFile: "certs/" + c.ID + ".crt", KeyFile: "certs/" + c.ID + ".key", NotAfter: pair.Leaf.NotAfter.Format(time.RFC3339)}
		})
		if err := m.cfg.State().Flush(); err != nil {
			return err
		}
		if index >= 0 {
			cfg.Certs[index] = c
		} else {
			cfg.Certs = append(cfg.Certs, c)
		}
		return nil
	})
	if err != nil {
		if stateChanged {
			m.cfg.State().Update(func(s *config.State) {
				if oldState == (config.CertState{}) {
					delete(s.Certs, c.ID)
				} else {
					s.Certs[c.ID] = oldState
				}
			})
			_ = m.cfg.State().Flush()
		}
		if wrote {
			if oldCertExists && oldKeyExists {
				if restoreErr := writeCertificatePair(certFile, keyFile, oldCert, oldKey); restoreErr != nil {
					return fmt.Errorf("rollback certificate files: %w", restoreErr)
				}
			} else {
				_ = os.Remove(certFile)
				_ = os.Remove(keyFile)
				if oldCertExists {
					_ = os.WriteFile(certFile, oldCert, 0o600)
				}
				if oldKeyExists {
					_ = os.WriteFile(keyFile, oldKey, 0o600)
				}
			}
		}
		return err
	}
	m.publishPair(c.ID, pair, certPEM, keyPEM)
	return nil
}

func certSource(c config.CertConf) string {
	if c.Source == "uploaded" {
		return "uploaded"
	}
	return "acme"
}
