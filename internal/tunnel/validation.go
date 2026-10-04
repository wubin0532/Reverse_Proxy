package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/webproxy"
)

var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var cloudIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var hostnamePattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func routeService(dir string, route config.TunnelRoute) string {
	if route.Target == "site" {
		return "unix:" + webproxy.TunnelSocketPath(dir, route.SiteID)
	}
	return route.URL
}

func validateInstance(inst *config.TunnelInstance) error {
	inst.Name = strings.TrimSpace(inst.Name)
	if inst.Name == "" || len(inst.Name) > 128 {
		return fmt.Errorf("实例名称不能为空或超过 128 字节")
	}
	if inst.Source != "managed" && inst.Source != "imported" {
		return fmt.Errorf("实例来源无效")
	}
	if inst.TunnelID != "" && !uuidPattern.MatchString(inst.TunnelID) {
		return fmt.Errorf("Tunnel ID 格式无效")
	}
	if inst.Protocol == "" {
		inst.Protocol = "auto"
	}
	if inst.IPVersion == "" {
		inst.IPVersion = "auto"
	}
	if inst.Protocol != "auto" && inst.Protocol != "http2" && inst.Protocol != "quic" {
		return fmt.Errorf("连接协议无效")
	}
	if inst.IPVersion != "auto" && inst.IPVersion != "4" && inst.IPVersion != "6" {
		return fmt.Errorf("出口 IP 版本无效")
	}
	if len(inst.Token) > 16384 || strings.ContainsAny(inst.Token, "\r\n\x00") {
		return fmt.Errorf("Tunnel Token 格式无效")
	}
	return nil
}

func (m *Manager) validateRoute(route *config.TunnelRoute) error {
	route.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(route.Hostname), "."))
	if len(route.Hostname) > 253 || !hostnamePattern.MatchString(route.Hostname) || net.ParseIP(route.Hostname) != nil {
		return fmt.Errorf("请填写完整域名，不支持通配符")
	}
	if !cloudIDPattern.MatchString(route.ZoneID) {
		return fmt.Errorf("Zone ID 格式无效")
	}
	m.cfg.RLock()
	for _, task := range m.cfg.DDNS {
		if task.Enabled {
			for _, host := range task.Domains {
				if strings.EqualFold(strings.TrimSuffix(host, "."), route.Hostname) {
					m.cfg.RUnlock()
					return fmt.Errorf("域名正在被启用的 DDNS 任务使用")
				}
			}
		}
	}
	if route.Target == "site" {
		for _, site := range m.cfg.Sites {
			if site.ID == route.SiteID && site.Enabled {
				m.cfg.RUnlock()
				route.URL = ""
				route.SkipTLSVerify = false
				return nil
			}
		}
		m.cfg.RUnlock()
		return fmt.Errorf("请选择已启用的 Web 站点")
	}
	m.cfg.RUnlock()
	if route.Target != "url" {
		return fmt.Errorf("路由目标类型无效")
	}
	u, err := url.Parse(route.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("目标必须是无凭据、查询参数或路径的 HTTP/HTTPS 服务地址")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("目标端口无效")
	}
	if p == m.adminPort {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
		if err != nil {
			return fmt.Errorf("无法确认目标是否为本机管理端口")
		}
		local, _ := net.InterfaceAddrs()
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsUnspecified() {
				return fmt.Errorf("禁止发布本机管理端口")
			}
			for _, addr := range local {
				_, network, _ := net.ParseCIDR(addr.String())
				if network != nil && strings.Split(addr.String(), "/")[0] == ip.IP.String() {
					return fmt.Errorf("禁止发布本机管理端口")
				}
			}
		}
	}
	route.SiteID = ""
	route.URL = strings.TrimSuffix(u.String(), "/")
	return nil
}
