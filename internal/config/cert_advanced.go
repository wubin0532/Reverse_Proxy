package config

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

type CertificateMapping struct {
	Name          string `json:"name"`
	ReloadService string `json:"reloadService,omitempty"`
}

var mappingName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (c *CertConf) ValidateAdvanced() error {
	if c.CATrustPEM != "" {
		pool := x509.NewCertPool()
		if len(c.CATrustPEM) > 65536 || !pool.AppendCertsFromPEM([]byte(c.CATrustPEM)) {
			return fmt.Errorf("ACME CA信任证书无效或超过64KiB")
		}
	}
	if c.EABEnabled {
		key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(c.EABHMAC, "="))
		if c.EABKeyID == "" || len(c.EABKeyID) > 512 || err != nil || len(key) < 16 || len(key) > 512 {
			return fmt.Errorf("EAB须有Key ID和有效base64url HMAC密钥")
		}
	}
	if c.PropagationTimeout < 0 || c.PropagationTimeout > 1800 || (c.PropagationTimeout > 0 && c.PropagationTimeout < 10) || c.PropagationInterval < 0 || c.PropagationInterval > 60 {
		return fmt.Errorf("传播超时须为10到1800秒，轮询为1到60秒；0沿用默认")
	}
	if c.PropagationTimeout > 0 && c.PropagationInterval > c.PropagationTimeout {
		return fmt.Errorf("传播轮询间隔不能超过超时")
	}
	if len(c.DNSResolvers) > 8 {
		return fmt.Errorf("递归DNS最多8个")
	}
	for _, server := range c.DNSResolvers {
		host, port, err := net.SplitHostPort(server)
		if err != nil {
			host = server
			port = "53"
		}
		n, e := strconv.Atoi(port)
		if net.ParseIP(host) == nil || e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("递归DNS须为IP或IP:端口")
		}
	}
	if len(c.Mappings) > 8 {
		return fmt.Errorf("证书映射最多8个")
	}
	seen := map[string]bool{}
	for _, m := range c.Mappings {
		if !mappingName.MatchString(m.Name) || seen[m.Name] {
			return fmt.Errorf("映射名称仅允许字母、数字、横线和下划线，不可重复")
		}
		seen[m.Name] = true
		switch m.ReloadService {
		case "", "nginx", "uhttpd", "haproxy":
		default:
			return fmt.Errorf("仅支持固定nginx/uhttpd/haproxy重载")
		}
	}
	return nil
}
