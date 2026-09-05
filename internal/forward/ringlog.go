package forward

import (
	"sync"
	"time"

	"andey-proxy/internal/logcenter"
)

// RingLog 固定容量的环形日志。
type RingLog struct {
	mu      sync.Mutex
	entries []string
	cap     int
}

func NewRingLog(capacity int) *RingLog {
	return &RingLog{cap: capacity}
}

func (r *RingLog) Add(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, time.Now().Format("2006-01-02 15:04:05")+" "+logcenter.Redact(msg))
	if len(r.entries) > r.cap {
		r.entries = r.entries[len(r.entries)-r.cap:]
	}
}

// Entries 返回日志副本，最新的在最后。
func (r *RingLog) Entries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.entries))
	copy(out, r.entries)
	return out
}
