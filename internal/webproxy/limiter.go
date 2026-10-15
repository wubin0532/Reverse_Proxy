package webproxy

import (
	"math"
	"sync"
	"time"

	"andey-proxy/internal/config"
)

const (
	maxRateLimitBuckets = 4096
	// ratePruneInterval 空闲桶清理的最短间隔。超过这个间隔才做一次全表扫描；
	// 早期实现在桶表饱和后每个请求都扫全表（最多 4096 项，持全局锁），
	// 属于可被大量伪造源地址放大的性能悬崖。
	ratePruneInterval = 30 * time.Second
)

type rateBucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

// ruleLimiter is a bounded, in-memory token bucket keyed by rule and direct
// peer IP. It deliberately ignores forwarded headers.
type ruleLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*rateBucket
	lastPrune time.Time
}

func newRuleLimiter() *ruleLimiter {
	return &ruleLimiter{buckets: make(map[string]*rateBucket)}
}

func (l *ruleLimiter) allow(rule *config.SubRule, ip string, now time.Time) (bool, int) {
	if rule.RateLimitRPS <= 0 {
		return true, 0
	}
	rate := float64(rule.RateLimitRPS)
	burst := rule.RateLimitBurst
	if burst <= 0 {
		burst = rule.RateLimitRPS * 2
	}
	if burst < 1 {
		burst = 1
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastPrune.IsZero() || now.Sub(l.lastPrune) >= ratePruneInterval {
		l.lastPrune = now
		l.pruneLocked(now)
	}
	key := rule.ID + "\x00" + ip
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxRateLimitBuckets {
			l.evictOldestLocked()
		}
		b = &rateBucket{tokens: float64(burst), updated: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.updated).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(float64(burst), b.tokens+elapsed*rate)
	}
	b.updated = now
	b.lastSeen = now
	if b.tokens < 1 {
		return false, 1
	}
	b.tokens--
	return true, 0
}

func (l *ruleLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-10 * time.Minute)
	for key, bucket := range l.buckets {
		if bucket.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// evictOldestLocked 在桶表饱和时淘汰最久未见的桶。只在表里随机采样
// rateEvictSample 项，避免每次新建 key 都做 O(4096) 扫描（饱和时是每请求）。
func (l *ruleLimiter) evictOldestLocked() {
	const rateEvictSample = 64
	var oldestKey string
	var oldest time.Time
	seen := 0
	for key, bucket := range l.buckets {
		if oldestKey == "" || bucket.lastSeen.Before(oldest) {
			oldestKey, oldest = key, bucket.lastSeen
		}
		seen++
		if seen >= rateEvictSample {
			break
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}
