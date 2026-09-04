// Package ddns 动态域名解析模块：DNS 服务商对接、IP 获取、定时调度与 API。
package ddns

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Provider DNS 服务商抽象。
type Provider interface {
	// UpsertRecord 创建或更新一条 A/AAAA 记录，返回变更说明
	UpsertRecord(ctx context.Context, domain, recordType, ip string, ttl int) (string, error)
	// QueryRecord 只读查询一条记录当前值，不存在时返回空字符串
	QueryRecord(ctx context.Context, domain, recordType string) (string, error)
}

// 常见复合后缀，拆分主域时按三段处理。
var compoundSuffixes = []string{
	"com.cn", "net.cn", "org.cn", "gov.cn", "edu.cn", "ac.cn",
	"co.uk", "org.uk", "ac.uk",
	"com.au", "net.au", "org.au",
	"com.hk", "net.hk", "org.hk",
	"com.tw", "net.tw", "org.tw",
	"co.jp", "com.jp", "net.jp",
	"com.sg", "com.my", "com.br", "com.mx", "co.kr", "co.nz",
}

// splitDomain 把完整域名拆成子域前缀与主域。
// home.example.com -> ("home", "example.com")；example.com -> ("@", "example.com")。
func splitDomain(fqdn string) (rr, root string, err error) {
	fqdn = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(fqdn)), ".")
	labels := strings.Split(fqdn, ".")
	if len(labels) < 2 {
		return "", "", fmt.Errorf("域名格式不正确: %s", fqdn)
	}
	suffixLen := 2
	if len(labels) >= 3 {
		last2 := labels[len(labels)-2] + "." + labels[len(labels)-1]
		for _, cs := range compoundSuffixes {
			if last2 == cs {
				suffixLen = 3
				break
			}
		}
	}
	if len(labels) < suffixLen {
		return "", "", fmt.Errorf("域名格式不正确: %s", fqdn)
	}
	root = strings.Join(labels[len(labels)-suffixLen:], ".")
	if len(labels) == suffixLen {
		return "@", root, nil
	}
	rr = strings.Join(labels[:len(labels)-suffixLen], ".")
	if rr == "" {
		return "", "", fmt.Errorf("域名格式不正确: %s", fqdn)
	}
	return rr, root, nil
}

// errNotImplemented 暂未实现的能力。
var errNotImplemented = errors.New("该功能暂未实现")
