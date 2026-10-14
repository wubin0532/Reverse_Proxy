package acme

import (
	"crypto/tls"
	"fmt"
	"os"
)

// CertificateByID makes no-SNI bound listeners use the same pair lock as SNI
// supply and imports. Do not expose a second, unsynchronized disk reader.
func (m *Manager) CertificateByID(id string) (*tls.Certificate, error) {
	c, ok := m.findCert(id)
	if !ok || !c.Enabled {
		return nil, fmt.Errorf("绑定证书不可用: %w", os.ErrNotExist)
	}
	pair, err := m.loadCached(&c)
	if err != nil {
		return nil, err
	}
	leaf, err := certificateLeaf(pair)
	if err != nil {
		return nil, err
	}
	if !coversDomains(pair, c.Domains) {
		return nil, fmt.Errorf("绑定证书未覆盖配置域名: %w", os.ErrNotExist)
	}
	copy := *pair
	copy.Leaf = leaf
	return &copy, nil
}
