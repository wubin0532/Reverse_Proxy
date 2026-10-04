package acme

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
)

func certificateLeaf(pair *tls.Certificate) (*x509.Certificate, error) {
	if pair == nil || len(pair.Certificate) == 0 {
		return nil, errors.New("证书内容为空")
	}
	if pair.Leaf != nil {
		return pair.Leaf, nil
	}
	return x509.ParseCertificate(pair.Certificate[0])
}

// A requested wildcard must itself be signed; a certificate covering just
// one hostname is insufficient. Concrete names use standard SAN matching.
func coversDomains(pair *tls.Certificate, domains []string) bool {
	leaf, err := certificateLeaf(pair)
	if err != nil {
		return false
	}
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(domain), ".")
		if strings.HasPrefix(domain, "*.") {
			found := false
			for _, san := range leaf.DNSNames {
				if strings.EqualFold(strings.TrimSuffix(san, "."), domain) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		} else if leaf.VerifyHostname(domain) != nil {
			return false
		}
	}
	return true
}
