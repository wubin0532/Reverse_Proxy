package ddns

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"andey-proxy/internal/config"
)

// GetIP 按任务配置获取当前公网 IP。
func GetIP(ctx context.Context, task config.DDNSTask) (string, error) {
	switch task.IPSource {
	case "interface":
		if task.Interface == "" {
			return "", fmt.Errorf("任务 %s 未配置网卡名", task.Name)
		}
		iface, err := net.InterfaceByName(task.Interface)
		if err != nil {
			return "", fmt.Errorf("网卡 %s 不存在: %w", task.Interface, err)
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return "", err
		}
		return pickFromAddrs(addrs, task.IPType == "ipv6")
	case "api":
		if task.APIURL == "" {
			return "", fmt.Errorf("任务 %s 未配置 IP 查询地址", task.Name)
		}
		return fetchIPFromAPI(ctx, task.APIURL, task.IPType == "ipv6")
	case "webhook":
		return "", errNotImplemented
	default:
		return "", fmt.Errorf("未知的 IP 来源: %s", task.IPSource)
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

// fetchIPFromAPI 通过 HTTP 接口获取纯文本 IP。
func fetchIPFromAPI(ctx context.Context, rawURL string, wantV6 bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("IP 查询接口返回 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	return validateIP(strings.TrimSpace(string(body)), wantV6)
}

// validateIP 校验字符串是合法 IP 且类型匹配。
func validateIP(s string, wantV6 bool) (string, error) {
	s = strings.TrimSpace(s)
	ip := net.ParseIP(s)
	if ip == nil {
		return "", fmt.Errorf("接口返回的内容不是合法 IP: %q", s)
	}
	isV4 := ip.To4() != nil
	if wantV6 && isV4 {
		return "", fmt.Errorf("接口返回的不是 IPv6 地址: %q", s)
	}
	if !wantV6 && !isV4 {
		return "", fmt.Errorf("接口返回的不是 IPv4 地址: %q", s)
	}
	return ip.String(), nil
}
