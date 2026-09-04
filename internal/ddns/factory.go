package ddns

import (
	"fmt"

	"andey-proxy/internal/config"
)

// NewProvider 按 Type 创建 DNS 服务商实例。
func NewProvider(conf config.DNSProviderConf) (Provider, error) {
	switch conf.Type {
	case "aliyun":
		return newAliyunProvider(conf), nil
	case "cloudflare":
		return newCloudflareProvider(conf), nil
	case "dnspod":
		return newDnspodProvider(conf), nil
	default:
		return nil, fmt.Errorf("不支持的 DNS 服务商类型: %s", conf.Type)
	}
}
