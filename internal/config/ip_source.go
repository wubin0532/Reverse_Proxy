package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func ValidateIPSource(s IPSourceConfig, ipv6 bool) error {
	if len(s.Fallbacks) > 3 {
		return fmt.Errorf("最多配置3个回退来源")
	}
	for _, fallback := range s.Fallbacks {
		if len(fallback.Fallbacks) != 0 {
			return fmt.Errorf("回退来源不能嵌套")
		}
		if err := ValidateIPSource(fallback, ipv6); err != nil {
			return err
		}
	}
	if s.Source == "interface" {
		if len(s.Interface) > 64 || strings.ContainsAny(s.Interface, "\r\n\x00") {
			return fmt.Errorf("网卡名称无效")
		}
		return nil
	}
	u, err := url.Parse(s.APIURL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || len(s.APIURL) > 2048 {
		return fmt.Errorf("IP取源地址无效")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("取源端口无效")
		}
	}
	switch s.Source {
	case "api":
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("HTTP取源地址无效")
		}
		ip := net.ParseIP(u.Hostname())
		if u.Scheme == "http" && u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("远程HTTP取源须使用HTTPS")
		}
	case "stun":
		if u.Scheme != "stun" || u.Path != "" || u.RawQuery != "" {
			return fmt.Errorf("STUN格式为stun://server[:port]")
		}
	case "dns":
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if u.Scheme != "dns" || u.RawQuery != "" || len(parts) != 2 || parts[0] == "" || len(parts[0]) > 253 || strings.ContainsAny(parts[0], " \r\n\\") {
			return fmt.Errorf("DNS格式为dns://server[:port]/domain/A-or-AAAA-or-TXT")
		}
		typ := strings.ToUpper(parts[1])
		if typ != "TXT" && ((!ipv6 && typ != "A") || (ipv6 && typ != "AAAA")) {
			return fmt.Errorf("DNS查询类型与地址族不一致")
		}
	default:
		return fmt.Errorf("取源类型须为interface、api、stun或dns")
	}
	return nil
}
