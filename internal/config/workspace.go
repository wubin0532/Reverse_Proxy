package config

import (
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

// Existing tasks keep their legacy fields; new tasks may manage both families.
type DDNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
	Domain  string `json:"domain"`
	Type    string `json:"type"`            // A, AAAA, CNAME or TXT
	Value   string `json:"value,omitempty"` // Literal value for CNAME/TXT; A/AAAA use IP sources.
	TTL     int    `json:"ttl"`
}

type IPSourceConfig struct {
	Fallbacks []IPSourceConfig `json:"fallbacks,omitempty"`
	Source    string           `json:"source"` // interface or api
	Interface string           `json:"interface"`
	APIURL    string           `json:"apiUrl"`
}

type ProtectionPolicy struct {
	WAF        *WAFConfig `json:"waf,omitempty"`
	CC         *CCConfig  `json:"cc,omitempty"`
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	Global     bool       `json:"global"`
	IPListMode string     `json:"ipListMode"`
	IPList     []string   `json:"ipList"`
	UAListMode string     `json:"uaListMode"`
	UAList     []string   `json:"uaList"`
}

type DefaultResponse struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
}

// RemainingDays is the single display convention used by certificates and overview.
func RemainingDays(notAfter string, now time.Time) *int {
	expiry, err := time.Parse(time.RFC3339, notAfter)
	if err != nil {
		return nil
	}
	n := int(math.Floor(expiry.Sub(now).Hours() / 24))
	return &n
}

// NormalizeWorkspace is shared by load, import and mutations. Older configs
// retain their local rule behavior; no policy binding is created implicitly.
func (c *Config) NormalizeWorkspace() error {
	if c.TCPTrap != nil {
		if err := c.ValidateTrap(c.TCPTrap); err != nil {
			return err
		}
	}
	if err := c.normalizeAccessGroups(); err != nil {
		return err
	}
	if err := c.normalizeBackendPools(); err != nil {
		return err
	}
	if c.SchemaVersion > 2 || c.SchemaVersion < 0 {
		return fmt.Errorf("不支持的配置版本")
	}
	c.SchemaVersion = 2
	if len(c.ProtectionPolicies) > 256 {
		return fmt.Errorf("防护策略最多256条")
	}
	known := map[string]bool{}
	for _, p := range c.ProtectionPolicies {
		if err := p.ValidateExtensions(); err != nil {
			return err
		}
		if p.ID == "" || known[p.ID] {
			return fmt.Errorf("防护策略ID为空或重复")
		}
		known[p.ID] = true
		if strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 {
			return fmt.Errorf("防护策略名称无效")
		}
		for _, m := range []string{p.IPListMode, p.UAListMode} {
			if m != "" && m != "off" && m != "blacklist" && m != "whitelist" {
				return fmt.Errorf("防护名单模式无效")
			}
		}
		if len(p.IPList) > 4096 || len(p.UAList) > 256 {
			return fmt.Errorf("防护名单超过容量")
		}
		if p.IPListMode == "whitelist" && len(p.IPList) == 0 || p.UAListMode == "whitelist" && len(p.UAList) == 0 {
			return fmt.Errorf("防护白名单不能为空")
		}
		for _, ip := range p.IPList {
			if _, _, err := net.ParseCIDR(strings.TrimSpace(ip)); err != nil && net.ParseIP(strings.TrimSpace(ip)) == nil {
				return fmt.Errorf("防护IP或CIDR无效")
			}
		}
		for _, ua := range p.UAList {
			if strings.TrimSpace(ua) == "" || len(ua) > 256 {
				return fmt.Errorf("防护UA关键词无效")
			}
		}
	}
	check := func(id string) error {
		if id != "" && !known[id] {
			return fmt.Errorf("引用的防护策略不存在")
		}
		return nil
	}
	for _, s := range c.Sites {
		if d := s.DefaultResponse; d != nil && (d.StatusCode < 200 || d.StatusCode > 599 || d.StatusCode >= 300 && d.StatusCode < 400 || len(d.Body) > 65536 || d.StatusCode == 204 && d.Body != "") {
			return fmt.Errorf("默认响应无效")
		}

		if err := check(s.PolicyID); err != nil {
			return err
		}
		for _, r := range s.Rules {
			if err := r.ValidateAdvanced(); err != nil {
				return err
			}
			if len(r.Group) > 128 {
				return fmt.Errorf("规则分组过长")
			}
			if err := check(r.PolicyID); err != nil {
				return err
			}
		}
	}
	if err := validateServiceRoots(c.Sites); err != nil {
		return err
	}
	for _, f := range c.Forwards {
		if err := check(f.PolicyID); err != nil {
			return err
		}
	}
	for _, cert := range c.Certs {
		if err := cert.ValidateAdvanced(); err != nil {
			return err
		}
		if cert.Source != "" && cert.Source != "acme" && cert.Source != "uploaded" {
			return fmt.Errorf("证书来源无效")
		}
		if cert.Source != "uploaded" && cert.KeyType != "" && cert.KeyType != "P256" && cert.KeyType != "P384" && cert.KeyType != "RSA2048" && cert.KeyType != "RSA4096" {
			return fmt.Errorf("签发算法无效")
		}
	}
	for _, task := range c.DDNS {
		if len(task.Records) > 100 {
			return fmt.Errorf("DDNS记录超过容量")
		}
		seen := map[string]bool{}
		targets := map[string]bool{}
		for i := range task.Records {
			record := &task.Records[i]
			if err := record.NormalizeStaticValue(); err != nil {
				return err
			}
			target := strings.ToLower(strings.TrimSuffix(record.Domain, ".")) + "|" + record.Type
			if record.ID == "" || seen[record.ID] || strings.Contains(record.ID, "|") || record.Domain == "" || targets[target] || record.TTL < 0 || record.TTL > 86400 {
				return fmt.Errorf("DDNS记录无效或重复")
			}
			seen[record.ID] = true
			targets[target] = true
		}
		for i, source := range []*IPSourceConfig{task.IPv4Source, task.IPv6Source} {
			if source != nil {
				if err := ValidateIPSource(*source, i == 1); err != nil {
					return err
				}
			}
		}

	}
	for i := range c.DDNS {
		if c.DDNS[i].IPSource == "webhook" {
			c.DDNS[i].Enabled = false
			c.DDNS[i].IPSource = "interface"
			c.DDNS[i].Interface = "auto"
		}
	}
	return nil
}
