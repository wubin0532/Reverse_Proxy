package acme

import (
	"context"
	"strings"
	"testing"

	"andey-proxy/internal/config"
)

// TestObtainNotifyFailure 申请失败时推送 cert 事件通知，内容含证书名、域名与错误。
func TestObtainNotifyFailure(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Lock()
	cfg.Certs = append(cfg.Certs, config.CertConf{
		ID: "c1", Name: "测试证书", Enabled: true,
		Domains: []string{"a.example.com"}, ProviderID: "missing",
	})
	cfg.Unlock()
	m := NewManager(cfg)

	var calls []string
	m.Notify = func(event, title, content string) {
		calls = append(calls, event+"|"+title+"|"+content)
	}

	// 凭据不存在，申请必然失败
	if err := m.Obtain(context.Background(), "c1"); err == nil {
		t.Fatal("凭据缺失应申请失败")
	}
	if len(calls) != 1 {
		t.Fatalf("失败应通知一次，实际 %d 次", len(calls))
	}
	parts := strings.SplitN(calls[0], "|", 3)
	if parts[0] != "cert" {
		t.Errorf("事件类型错误: %s", parts[0])
	}
	if !strings.Contains(parts[1], "证书申请失败") {
		t.Errorf("标题错误: %s", parts[1])
	}
	if !strings.Contains(parts[2], "测试证书") || !strings.Contains(parts[2], "a.example.com") {
		t.Errorf("内容缺少证书名或域名: %s", parts[2])
	}
}

// TestObtainNotifyNilSafe 未设置回调时申请失败不 panic、不通知。
func TestObtainNotifyNilSafe(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Lock()
	cfg.Certs = append(cfg.Certs, config.CertConf{
		ID: "c2", Name: "x", Enabled: true,
		Domains: []string{"a.example.com"}, ProviderID: "missing",
	})
	cfg.Unlock()
	m := NewManager(cfg)
	if err := m.Obtain(context.Background(), "c2"); err == nil {
		t.Fatal("凭据缺失应申请失败")
	}
}
