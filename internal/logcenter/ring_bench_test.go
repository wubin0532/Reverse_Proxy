package logcenter

import (
	"testing"
	"time"
)

// 环形缓冲写满后必须零搬移：旧实现每条日志都会复制 maxMemoryEntries 个 Entry
// （访问日志是逐请求写入的），这是当时最严重的热路径开销。
func BenchmarkEntryRingAppendAtCapacity(b *testing.B) {
	r := &entryRing{}
	e := Entry{Time: time.Now(), Level: "info", Source: "webproxy", Message: "192.0.2.4 /api/x 200 12ms"}
	for i := 0; i < maxMemoryEntries; i++ {
		r.append(e, maxMemoryEntries)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.append(e, maxMemoryEntries)
	}
}

// 脱敏必须对不含敏感关键词的访问日志走廉价路径（不跑正则）。
func BenchmarkRedactAccessLine(b *testing.B) {
	line := "192.0.2.4 /api/v1/sites/abc/rules 200 12ms"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := Redact(line); got != line {
			b.Fatalf("访问日志被误改: %q", got)
		}
	}
}

// 含凭据的行仍必须完整脱敏（预筛不得放过）。
func BenchmarkRedactSecretLine(b *testing.B) {
	line := "client 10.0.0.1 Authorization: Bearer CANARY token=abcdef"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Redact(line)
	}
}
