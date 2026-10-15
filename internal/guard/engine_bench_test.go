package guard

import (
	"testing"

	"andey-proxy/internal/config"
)

func benchEngine(b *testing.B) *Engine {
	b.Helper()
	cfg, err := config.Load(b.TempDir() + "/cfg")
	if err != nil {
		b.Fatal(err)
	}
	e := NewEngine(cfg)
	b.Cleanup(func() {
		e.Trap.Stop()
		cfg.State().Close()
	})
	return e
}

// 每个 TCP 连接 / 每个 UDP 包都会调用 CheckGlobal，必须保持常数开销。
func BenchmarkCheckGlobal(b *testing.B) {
	e := benchEngine(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !e.CheckGlobal("192.0.2.1", "curl/8", "web", "site-1") {
			b.Fatal("无策略时不应拒绝")
		}
	}
}

// 策略过滤在"无全局策略且未命中"时必须零分配。
func BenchmarkHTTPPoliciesEmpty(b *testing.B) {
	e := benchEngine(b)
	ids := []string{"", ""}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := e.httpPolicies(ids); got != nil {
			b.Fatalf("无策略时应返回 nil, got %v", got)
		}
	}
}
