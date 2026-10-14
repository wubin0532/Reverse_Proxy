package config

import (
	"fmt"
	"net/url"
	"strings"
)

type BackendNode struct {
	URL           string `json:"url"`
	Enabled       bool   `json:"enabled"`
	Weight        int    `json:"weight"`
	MaxConcurrent int    `json:"maxConcurrent"`
}

type BackendPool struct {
	ID            string          `json:"id"`
	Revision      uint64          `json:"revision"`
	Name          string          `json:"name"`
	Nodes         []BackendNode   `json:"nodes"`
	MaxConcurrent int             `json:"maxConcurrent"`
	HealthCheck   HealthCheckConf `json:"healthCheck"`
}

func (p *BackendPool) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 128 || len(p.Nodes) < 1 || len(p.Nodes) > 32 {
		return fmt.Errorf("后端池须有名称及1到32个节点")
	}
	if p.MaxConcurrent < 0 || p.MaxConcurrent > 4096 {
		return fmt.Errorf("池并发上限必须为0到4096；0使用4096")
	}
	seen := map[string]bool{}
	for i := range p.Nodes {
		n := &p.Nodes[i]
		n.URL = strings.TrimSpace(n.URL)
		u, err := url.Parse(n.URL)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || seen[n.URL] {
			return fmt.Errorf("节点地址须为不重复且不含凭据的HTTP(S)地址")
		}
		seen[n.URL] = true
		if n.Weight < 1 || n.Weight > 100 || n.MaxConcurrent < 0 || n.MaxConcurrent > 4096 {
			return fmt.Errorf("权重必须为1到100，节点并发上限为0到4096")
		}
	}
	return p.HealthCheck.Validate()
}

func (c *Config) normalizeBackendPools() error {
	if len(c.BackendPools) > 128 {
		return fmt.Errorf("后端池最多128个")
	}
	known := map[string]bool{}
	for i := range c.BackendPools {
		p := &c.BackendPools[i]
		if p.ID == "" || known[p.ID] {
			return fmt.Errorf("后端池ID为空或重复")
		}
		known[p.ID] = true
		if err := p.Validate(); err != nil {
			return err
		}
	}
	for _, s := range c.Sites {
		for _, r := range s.Rules {
			if r.BackendPoolID != "" && (r.Type != "reverse" || !known[r.BackendPoolID]) {
				return fmt.Errorf("反代引用的后端池不存在")
			}
		}
	}
	return nil
}
