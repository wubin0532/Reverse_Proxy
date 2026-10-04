package acme

import (
	"context"
	"log"
	"net"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
)

// txtPropagationCheck 生成 DNS-01 传播检查函数：绕过本地 DNS 与权威 NS 直连，
// 只向指定公共递归服务器查询 TXT 值，任一服务器返回期望值即视为已生效。
func txtPropagationCheck(parent context.Context, servers []string) dns01.WrapPreCheckFunc {
	return func(_, fqdn, value string, _ dns01.PreCheckFunc) (bool, error) {
		name := strings.TrimSuffix(fqdn, ".")
		deadline := time.Now().Add(2 * time.Minute)
		for {
			if err := parent.Err(); err != nil {
				return false, err
			}
			for _, srv := range servers {
				resolver := &net.Resolver{
					PreferGo: true,
					Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
						d := net.Dialer{Timeout: 5 * time.Second}
						return d.DialContext(ctx, "udp", srv+":53")
					},
				}
				ctx, cancel := context.WithTimeout(parent, 6*time.Second)
				txts, err := resolver.LookupTXT(ctx, name)
				cancel()
				if err != nil {
					log.Printf("[acme] 传播检查查询 %s 经 %s 失败: %v", name, srv, err)
					continue
				}
				for _, txt := range txts {
					if txt == value {
						return true, nil
					}
				}
			}
			if time.Now().After(deadline) {
				return false, nil
			}
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-parent.Done():
				timer.Stop()
				return false, parent.Err()
			case <-timer.C:
			}
		}
	}
}
