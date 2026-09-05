// Package guard 提供 IP/User-Agent 黑白名单匹配，供端口转发与 Web 服务共用。
package guard

import (
	"net"
	"strings"
)

// AllowIP 按名单模式判断 IP 是否放行。
// mode: "" 或 "off" 放行全部；"whitelist" 仅名单内放行；"blacklist" 名单内拒绝。
func AllowIP(mode string, list []string, ip string) bool {
	return CompileIP(mode, list).Allow(ip)
}

// IPMatcher is immutable and safe for concurrent requests. Compile once when a
// rule starts or changes, rather than parsing every CIDR for every packet.
type IPMatcher struct {
	mode      string
	networks  []*net.IPNet
	addresses []net.IP
}

func CompileIP(mode string, list []string) *IPMatcher {
	m := &IPMatcher{mode: mode}
	if mode != "whitelist" && mode != "blacklist" {
		return m
	}
	for _, raw := range list {
		value := strings.TrimSpace(raw)
		if _, network, err := net.ParseCIDR(value); err == nil {
			m.networks = append(m.networks, network)
		} else if ip := net.ParseIP(value); ip != nil {
			m.addresses = append(m.addresses, ip)
		}
	}
	return m
}

func (m *IPMatcher) Allow(ip string) bool {
	if m.mode != "whitelist" && m.mode != "blacklist" {
		return true
	}
	parsed := net.ParseIP(ip)
	matched := false
	if parsed != nil {
		for _, network := range m.networks {
			if network.Contains(parsed) {
				matched = true
				break
			}
		}
		if !matched {
			for _, address := range m.addresses {
				if address.Equal(parsed) {
					matched = true
					break
				}
			}
		}
	}
	if m.mode == "whitelist" {
		return matched
	}
	return !matched
}

// AllowUA 按 User-Agent 关键字名单判断（包含匹配，不区分大小写）。
func AllowUA(mode string, list []string, ua string) bool {
	if mode != "whitelist" && mode != "blacklist" {
		return true
	}
	lower := strings.ToLower(ua)
	matched := false
	for _, kw := range list {
		if kw != "" && strings.Contains(lower, strings.ToLower(strings.TrimSpace(kw))) {
			matched = true
			break
		}
	}
	if mode == "whitelist" {
		return matched
	}
	return !matched
}
