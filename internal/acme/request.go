package acme

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"andey-proxy/internal/config"
)

var errRequestChanged = errors.New("证书申请配置已变更，已丢弃旧申请结果")

// A response body must retain the operation context until it is consumed or
// closed. Canceling immediately after RoundTrip would truncate downloads.
type operationTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t operationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	release := func() { stop(); cancel() }
	res, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		release()
		return nil, err
	}
	res.Body = &operationBody{ReadCloser: res.Body, release: release}
	return res, nil
}

type operationBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *operationBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func operationHTTPClient(ctx context.Context, client *http.Client) *http.Client {
	out := *client
	base := out.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out.Transport = operationTransport{ctx: ctx, base: base}
	return &out
}

// Reserve both the certificate and its lifecycle before returning from an
// asynchronous API call. Stop excludes new reservations and waits for all work.
func (m *Manager) ObtainAsync(id string) error {
	if !m.beginObtain(id) {
		return errors.New("该证书正在申请中或管理器已停止")
	}
	go func() {
		defer m.endObtain(id)
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Minute)
		defer cancel()
		_ = m.runObtain(ctx, id)
	}()
	return nil
}

func (m *Manager) requestMatchesLocked(cert config.CertConf, provider config.DNSProviderConf) bool {
	certMatches := false
	for _, current := range m.cfg.Certs {
		if current.ID == cert.ID {
			certMatches = current.Enabled == cert.Enabled && sameDomains(current.Domains, cert.Domains) &&
				current.Email == cert.Email && current.CADirURL == cert.CADirURL && current.ProviderID == cert.ProviderID
			break
		}
	}
	if !certMatches {
		return false
	}
	for _, current := range m.cfg.Providers {
		if current.ID == provider.ID {
			return current.Type == provider.Type && current.Key == provider.Key && current.Secret == provider.Secret && current.Endpoint == provider.Endpoint
		}
	}
	if provider.ID == "" {
		for _, current := range m.cfg.Providers {
			if current.ID == cert.ProviderID {
				return false
			}
		}
		return true
	}
	return false
}

// Keep the configuration read lock across publishing files and state so a
// domain edit, deletion or restore cannot commit an obsolete result afterward.
func (m *Manager) commitCertificate(ctx context.Context, cert config.CertConf, provider config.DNSProviderConf, certPEM, keyPEM []byte) (string, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return "", err
	}
	if !coversDomains(&pair, cert.Domains) {
		return "", errors.New("签发证书未覆盖申请的全部域名")
	}
	notAfter, err := parseNotAfter(certPEM)
	if err != nil {
		return "", err
	}
	m.cfg.RLock()
	defer m.cfg.RUnlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !m.requestMatchesLocked(cert, provider) {
		return "", errRequestChanged
	}
	certFile, keyFile := m.certPath(&cert)
	if err := writeCertificatePair(certFile, keyFile, certPEM, keyPEM); err != nil {
		return "", err
	}
	result := notAfter.Format(time.RFC3339)
	m.writeResult(cert.ID, result, "")
	m.invalidate(cert.ID)
	return result, nil
}

func (m *Manager) setRequestError(cert config.CertConf, provider config.DNSProviderConf, message string) {
	m.cfg.RLock()
	defer m.cfg.RUnlock()
	if m.requestMatchesLocked(cert, provider) {
		m.writeResult(cert.ID, "", message)
	}
}
