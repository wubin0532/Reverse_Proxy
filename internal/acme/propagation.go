package acme

import (
	"context"
	"github.com/go-acme/lego/v4/challenge"
	"log"
	"net"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
)

// txtPropagationCheck 生成 DNS-01 传播检查函数：绕过本地 DNS 与权威 NS 直连，
// 只向指定公共递归服务器查询 TXT 值，任一服务器返回期望值即视为已生效。
func txtPropagationCheck(parent context.Context, servers []string) dns01.WrapPreCheckFunc {
	return txtPropagationCheckOptions(parent, servers, 2*time.Minute, 5*time.Second)
}

type timedDNSProvider struct {
	challenge.Provider
	timeout, interval time.Duration
}

func (p timedDNSProvider) Timeout() (time.Duration, time.Duration) { return p.timeout, p.interval }

func txtPropagationCheckOptions(parent context.Context, servers []string, timeout, interval time.Duration) dns01.WrapPreCheckFunc {
	return func(_, fqdn, value string, _ dns01.PreCheckFunc) (bool, error) {
		operation, cancelOperation := context.WithTimeout(parent, timeout)
		defer cancelOperation()
		name := strings.TrimSuffix(fqdn, ".")
		deadline := time.Now().Add(timeout)
		for {
			if err := operation.Err(); err != nil {
				return false, err
			}
			for _, srv := range servers {
				resolver := &net.Resolver{
					PreferGo: true,
					// 必须使用 net 解析器传入的 network：TC 置位的应答会触发
					// TCP 重试，若第二次仍返回 UDP Conn 会被当成完整应答，导致
					// 传播检查永远判定未生效（同名多条 TXT + DNSSEC 时必现）。
					Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
						d := net.Dialer{Timeout: 5 * time.Second}
						return d.DialContext(ctx, network, resolverAddress(srv))
					},
				}
				ctx, cancel := context.WithTimeout(operation, 6*time.Second)
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
			timer := time.NewTimer(interval)
			select {
			case <-operation.Done():
				timer.Stop()
				return false, operation.Err()
			case <-timer.C:
			}
		}
	}
}

func resolverAddress(s string) string {
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return net.JoinHostPort(s, "53")
}
