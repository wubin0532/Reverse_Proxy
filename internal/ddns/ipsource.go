package ddns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"andey-proxy/internal/config"
)

// GetIP 按任务配置获取当前公网 IP。
func GetIP(ctx context.Context, task config.DDNSTask) (string, error) {
	ip, _, err := GetIPDetail(ctx, task)
	return ip, err
}

// GetIPDetail 同 GetIP，额外返回实际使用的网卡名（自动识别时为识别结果）。
func GetIPDetail(ctx context.Context, task config.DDNSTask) (ip, ifaceName string, err error) {
	switch task.IPSource {
	case "interface":
		ifaceName = task.Interface
		if ifaceName == "" || ifaceName == "auto" {
			resolved, rerr := resolveWANInterface(task.IPType == "ipv6")
			if rerr != nil {
				if task.Name != "" {
					return "", "", fmt.Errorf("任务 %s: %w", task.Name, rerr)
				}
				return "", "", rerr
			}
			ifaceName = resolved
		}
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil {
			return "", "", fmt.Errorf("网卡 %s 不存在: %w", ifaceName, err)
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return "", "", err
		}
		ip, err = pickFromAddrs(addrs, task.IPType == "ipv6")
		if err != nil {
			return "", "", err
		}
		return ip, ifaceName, nil
	case "api":
		if task.APIURL == "" {
			return "", "", fmt.Errorf("任务 %s 未配置 IP 查询地址", task.Name)
		}
		ip, err := fetchIPFromAPI(ctx, task.APIURL, task.IPType == "ipv6")
		return ip, "", err
	default:
		return "", "", fmt.Errorf("未知的 IP 来源: %s", task.IPSource)
	}
}

// pickFromAddrs 从网卡地址列表中挑选符合条件的 IP。
func pickFromAddrs(addrs []net.Addr, ipv6 bool) (string, error) {
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}
		if ipv6 {
			if ip.To4() != nil {
				continue
			}
			if !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() || isULA(ip) {
				continue
			}
			return ip.String(), nil
		}
		if ip4 := ip.To4(); ip4 != nil {
			if ip4.IsGlobalUnicast() && !ip4.IsLoopback() {
				return ip4.String(), nil
			}
		}
	}
	if ipv6 {
		return "", fmt.Errorf("网卡上没有可用的公网 IPv6 地址")
	}
	return "", fmt.Errorf("网卡上没有可用的 IPv4 地址")
}

// isULA 判断是否为 IPv6 本地唯一地址 fc00::/7。
func isULA(ip net.IP) bool {
	b := ip.To16()
	return b != nil && ip.To4() == nil && b[0]&0xfe == 0xfc
}

// publicDNSServers 本地 DNS 被代理软件劫持（fake-ip/NXDOMAIN）时使用的公共递归 DNS。
var publicDNSServers = []string{"223.5.5.5", "119.29.29.29", "1.1.1.1"}

// publicDNSClient 绕过系统解析器的 HTTP 客户端，域名直接经公共递归 DNS 解析。
var publicDNSClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if net.ParseIP(host) != nil {
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
			}
			var lastErr error
			for _, srv := range publicDNSServers {
				resolver := &net.Resolver{
					PreferGo: true,
					Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
						return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", srv+":53")
					},
				}
				ips, err := resolver.LookupIP(ctx, "ip", host)
				if err != nil || len(ips) == 0 {
					lastErr = err
					continue
				}
				d := &net.Dialer{Timeout: 10 * time.Second}
				return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
			}
			return nil, fmt.Errorf("公共 DNS 解析 %s 失败: %v", host, lastErr)
		},
	},
}

var ipAPIClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// fetchIPFromAPI 通过 HTTP 接口获取纯文本 IP。
// 本地 DNS 解析失败（如被代理软件劫持为 fake-ip/NXDOMAIN）时，自动改用公共 DNS 重试。
func fetchIPFromAPI(ctx context.Context, rawURL string, wantV6 bool) (string, error) {
	body, err := httpGetText(ctx, ipAPIClient, rawURL)
	if err != nil && isDNSNotFound(err) {
		log.Printf("[DDNS] 本地 DNS 解析失败，改用公共 DNS 重试 %s", safeURLForLog(rawURL))
		body, err = httpGetText(ctx, publicDNSClient, rawURL)
	}
	if err != nil {
		return "", err
	}
	return validateIP(strings.TrimSpace(string(body)), wantV6)
}

func safeURLForLog(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "[invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// httpGetText 发起 GET 请求并返回文本内容。
func httpGetText(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", errors.New("IP 查询地址无效")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", safeRequestError("IP 查询接口", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("IP 查询接口返回 %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// isDNSNotFound 判断错误是否为 DNS 解析失败。
func isDNSNotFound(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound || dnsErr.IsTimeout
	}
	return strings.Contains(err.Error(), "no such host")
}

// validateIP 校验字符串是合法 IP 且类型匹配。
func validateIP(s string, wantV6 bool) (string, error) {
	s = strings.TrimSpace(s)
	ip := net.ParseIP(s)
	if ip == nil {
		return "", fmt.Errorf("接口返回的内容不是合法 IP")
	}
	isV4 := ip.To4() != nil
	if wantV6 && isV4 {
		return "", fmt.Errorf("接口返回的不是 IPv6 地址")
	}
	if !wantV6 && !isV4 {
		return "", fmt.Errorf("接口返回的不是 IPv4 地址")
	}
	return ip.String(), nil
}
