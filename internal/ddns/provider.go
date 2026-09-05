// Package ddns 动态域名解析模块：DNS 服务商对接、IP 获取、定时调度与 API。
package ddns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Provider DNS 服务商抽象。
type Provider interface {
	// UpsertRecord 创建或更新一条 A/AAAA 记录，返回变更说明
	UpsertRecord(ctx context.Context, domain, recordType, ip string, ttl int) (string, error)
	// QueryRecord 只读查询一条记录当前值，不存在时返回空字符串
	QueryRecord(ctx context.Context, domain, recordType string) (string, error)
}

// splitDomain 把完整域名拆成子域前缀与主域。
// home.example.com -> ("home", "example.com")；example.com -> ("@", "example.com")。
func splitDomain(fqdn string) (rr, root string, err error) {
	fqdn = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(fqdn)), ".")
	root, err = publicsuffix.EffectiveTLDPlusOne(fqdn)
	if err != nil {
		return "", "", fmt.Errorf("域名格式不正确: %s", fqdn)
	}
	if fqdn == root {
		return "@", root, nil
	}
	rr = strings.TrimSuffix(fqdn, "."+root)
	if rr == "" {
		return "", "", fmt.Errorf("域名格式不正确: %s", fqdn)
	}
	return rr, root, nil
}

func providerHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// safeRequestError removes request URLs because provider URLs can contain
// signatures or access identifiers.
func safeRequestError(provider string, err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s 请求失败: %v", provider, urlErr.Err)
	}
	return fmt.Errorf("%s 请求失败", provider)
}

func redactProviderMessage(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}
