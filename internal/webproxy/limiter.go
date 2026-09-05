package webproxy

import (
	"math"
	"sync"
	"time"

	"andey-proxy/internal/config"
)

const maxRateLimitBuckets = 4096

type rateBucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

// ruleLimiter is a bounded, in-memory token bucket keyed by rule and direct
// peer IP. It deliberately ignores forwarded headers.
type ruleLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
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
	if len(l.buckets) >= maxRateLimitBuckets {
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

func (l *ruleLimiter) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, bucket := range l.buckets {
		if oldest.IsZero() || bucket.lastSeen.Before(oldest) {
			oldestKey, oldest = key, bucket.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}
