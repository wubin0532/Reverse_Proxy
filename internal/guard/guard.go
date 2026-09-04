// Package guard 提供 IP/User-Agent 黑白名单匹配，供端口转发与 Web 服务共用。
package guard

import (
	"net"
	"strings"
)

// AllowIP 按名单模式判断 IP 是否放行。
// mode: "" 或 "off" 放行全部；"whitelist" 仅名单内放行；"blacklist" 名单内拒绝。
func AllowIP(mode string, list []string, ip string) bool {
	if mode != "whitelist" && mode != "blacklist" {
		return true
	}
	matched := matchIP(list, ip)
	if mode == "whitelist" {
		return matched
	}
	return !matched
}

func matchIP(list []string, ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(item); err == nil {
			if cidr.Contains(parsed) {
				return true
			}
			continue
		}
		if net.ParseIP(item) != nil && net.ParseIP(item).Equal(parsed) {
			return true
		}
	}
	return false
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
