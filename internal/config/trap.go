package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type TCPTrap struct {
	Enabled    bool     `json:"enabled"`
	ListenHost string   `json:"listenHost"`
	Ports      []int    `json:"ports"`
	AllowIPs   []string `json:"allowIps"`
	BanSeconds int      `json:"banSeconds"`
}

func (c *Config) ValidateTrap(t *TCPTrap) error {
	if t.ListenHost == "" {
		t.ListenHost = "0.0.0.0"
	}
	if net.ParseIP(t.ListenHost) == nil {
		return fmt.Errorf("诱捕监听地址必须是IPv4或IPv6")
	}
	if t.BanSeconds == 0 {
		t.BanSeconds = 3600
	}
	if t.BanSeconds < 1 || t.BanSeconds > 2592000 {
		return fmt.Errorf("诱捕封禁时长必须为1到2592000秒")
	}
	if len(t.Ports) > 20 || t.Enabled && len(t.Ports) == 0 {
		return fmt.Errorf("启用诱捕须填写1到20个TCP端口")
	}
	seen := map[int]bool{}
	for _, p := range t.Ports {
		if p < 1 || p > 65535 || seen[p] {
			return fmt.Errorf("诱捕端口必须为1到65535且不能重复")
		}
		seen[p] = true
		if !t.Enabled {
			continue
		}
		if p == c.Settings.AdminPort {
			return fmt.Errorf("诱捕端口不能占用管理端口")
		}
		listeners := []string{}
		for _, s := range c.Sites {
			if s.Enabled {
				listeners = append(listeners, s.Listen)
			}
		}
		for _, f := range c.Forwards {
			if f.Enabled && f.Proto != "udp" {
				listeners = append(listeners, f.Listen)
			}
		}
		for _, addr := range listeners {
			_, rawPort, err := net.SplitHostPort(addr)
			port, _ := strconv.Atoi(rawPort)
			if err == nil && port == p {
				return fmt.Errorf("诱捕端口%d与已启用业务监听冲突", p)
			}
		}
	}
	if len(t.AllowIPs) > 4096 {
		return fmt.Errorf("诱捕豁免名单最多4096项")
	}
	for i, raw := range t.AllowIPs {
		raw = strings.TrimSpace(raw)
		if _, _, err := net.ParseCIDR(raw); err != nil && net.ParseIP(raw) == nil {
			return fmt.Errorf("诱捕豁免地址必须是IP或CIDR")
		}
		t.AllowIPs[i] = raw
	}
	return nil
}
