package webproxy

import (
	"fmt"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

// 桶表饱和后每次 allow 都必须是 O(1)：旧实现在表满时每请求做一次
// O(maxRateLimitBuckets) 的全表清理，构成可被大量伪造源地址放大的性能悬崖。
func BenchmarkRuleLimiterAllowAtCapacity(b *testing.B) {
	l := newRuleLimiter()
	rule := &config.SubRule{ID: "r1", RateLimitRPS: 100000, RateLimitBurst: 100000}
	now := time.Now()
	for i := 0; i < maxRateLimitBuckets; i++ {
		l.allow(rule, fmt.Sprintf("10.%d.%d.%d", i/(256*256), (i/256)%256, i%256), now)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.allow(rule, "192.0.2.1", now)
	}
}
