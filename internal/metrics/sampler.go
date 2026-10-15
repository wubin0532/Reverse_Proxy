// Package metrics samples host counters once per process, independently of viewers.
package metrics

import (
	"net/http"
	"runtime"
	rmetrics "runtime/metrics"
	"sync"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

type hostCounters struct {
	total, idle, memory, available, resident, received, sent uint64
	iface                                                    string
	cpuOK, memoryOK, networkOK                               bool
}

type Snapshot struct {
	Time              time.Time `json:"time"`
	HostCPU           *float64  `json:"hostCpu"`
	MemoryPercent     *float64  `json:"memoryPercent"`
	MemoryTotal       uint64    `json:"memoryTotal"`
	MemoryUsed        uint64    `json:"memoryUsed"`
	ProcessResident   uint64    `json:"processResident"`
	ProcessHeap       uint64    `json:"processHeap"`
	Goroutines        int       `json:"goroutines"`
	Interface         string    `json:"interface"`
	BytesInPerSecond  *float64  `json:"bytesInPerSecond"`
	BytesOutPerSecond *float64  `json:"bytesOutPerSecond"`
	Received          uint64    `json:"received"`
	Sent              uint64    `json:"sent"`
	HostSupported     bool      `json:"hostSupported"`
	NetworkSupported  bool      `json:"networkSupported"`
}

type Point struct {
	Time              time.Time `json:"time"`
	CPU               *float64  `json:"cpu"`
	Memory            *float64  `json:"memory"`
	BytesInPerSecond  *float64  `json:"bytesInPerSecond"`
	BytesOutPerSecond *float64  `json:"bytesOutPerSecond"`
}

type Sampler struct {
	cfg            *config.Config
	persistent     bool
	lastFlush      time.Time
	persistError   string
	persistBlocked bool
	persistMu      sync.Mutex
	mu             sync.RWMutex
	latest         Snapshot
	previous       hostCounters
	previousAt     time.Time
	realtime       []Point
	minutes        []Point
	minute         time.Time
	stop           chan struct{}
	once           sync.Once
	wg             sync.WaitGroup
}

func NewSampler(configs ...*config.Config) *Sampler {
	s := &Sampler{stop: make(chan struct{})}
	if len(configs) > 0 {
		s.cfg = configs[0]
		s.ConfigurePersistence()
		if s.persistent {
			s.loadHistory()
		}
	}
	s.sample(time.Now(), readHost())
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case at := <-t.C:
				s.sample(at, readHost())
				s.flushHistory(false)
			case <-s.stop:
				return
			}
		}
	}()
	return s
}

func (s *Sampler) Stop() {
	s.once.Do(func() { close(s.stop); s.wg.Wait(); s.flushHistory(true) })
	s.wg.Wait()
}

// heapAllocSample 读取与 runtime.MemStats.HeapAlloc 等价的堆对象字节数。
// runtime.ReadMemStats 会触发 stop-the-world，每 5 秒一次在大堆上表现为周期性
// 停顿；runtime/metrics 读取同一指标且不停止世界。
func readHeapAlloc() uint64 {
	// Read mutates the sample buffer; independent samplers must not share it.
	samples := []rmetrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	rmetrics.Read(samples)
	if v := samples[0].Value; v.Kind() == rmetrics.KindUint64 {
		return v.Uint64()
	}
	return 0
}

func value(v float64) *float64 { return &v }
func appendBounded(points []Point, p Point, limit int) []Point {
	if len(points) >= limit {
		copy(points, points[1:])
		points = points[:len(points)-1]
	}
	return append(points, p)
}

func (s *Sampler) sample(at time.Time, h hostCounters) {
	heap := readHeapAlloc()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := Snapshot{Time: at, MemoryTotal: h.memory, ProcessResident: h.resident, ProcessHeap: heap, Goroutines: runtime.NumGoroutine(), Interface: h.iface, Received: h.received, Sent: h.sent, HostSupported: h.cpuOK || h.memoryOK, NetworkSupported: h.networkOK}
	if h.memoryOK && h.memory > 0 && h.available <= h.memory {
		n.MemoryUsed = h.memory - h.available
		n.MemoryPercent = value(float64(n.MemoryUsed) * 100 / float64(h.memory))
	}
	old := s.previous
	elapsed := at.Sub(s.previousAt).Seconds()
	if !s.previousAt.IsZero() && elapsed > 0 {
		if h.cpuOK && old.cpuOK && h.total > old.total && h.idle >= old.idle {
			total := h.total - old.total
			idle := h.idle - old.idle
			if idle <= total {
				n.HostCPU = value(float64(total-idle) * 100 / float64(total))
			}
		}
		if h.networkOK && old.networkOK && h.iface == old.iface && h.received >= old.received && h.sent >= old.sent {
			n.BytesInPerSecond = value(float64(h.received-old.received) / elapsed)
			n.BytesOutPerSecond = value(float64(h.sent-old.sent) / elapsed)
		}
	}
	s.latest = n
	s.previous = h
	s.previousAt = at
	p := Point{Time: at, CPU: n.HostCPU, Memory: n.MemoryPercent, BytesInPerSecond: n.BytesInPerSecond, BytesOutPerSecond: n.BytesOutPerSecond}
	for len(s.realtime) > 0 && !s.realtime[len(s.realtime)-1].Time.Before(at) {
		s.realtime = s.realtime[:len(s.realtime)-1]
	}
	for len(s.minutes) > 0 && s.minutes[len(s.minutes)-1].Time.After(at) {
		s.minutes = s.minutes[:len(s.minutes)-1]
	}
	s.realtime = appendBounded(s.realtime, p, 720)
	minute := at.Truncate(time.Minute)
	if minute != s.minute {
		s.minutes = appendBounded(s.minutes, p, 1440)
		s.minute = minute
	} else if len(s.minutes) > 0 {
		s.minutes[len(s.minutes)-1] = p
	}
}

func (s *Sampler) Snapshot() Snapshot { s.mu.RLock(); defer s.mu.RUnlock(); return s.latest }
func (s *Sampler) History(day bool) []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.realtime
	window := time.Hour
	if day {
		p = s.minutes
		window = 24 * time.Hour
	}
	cutoff := s.latest.Time.Add(-window)
	start := 0
	for start < len(p) && p[start].Time.Before(cutoff) {
		start++
	}
	return append([]Point{}, p[start:]...)
}

func RegisterRoutes(r chi.Router, s *Sampler) {
	s.registerHistorySettings(r)
	r.Get("/api/metrics", func(w http.ResponseWriter, _ *http.Request) { api.OK(w, s.Snapshot()) })
	r.Get("/api/metrics/history", func(w http.ResponseWriter, q *http.Request) {
		rangeName := q.URL.Query().Get("range")
		if rangeName != "" && rangeName != "1h" && rangeName != "24h" {
			api.Fail(w, 400, "range must be 1h or 24h")
			return
		}
		sampleSeconds := 5
		if rangeName == "24h" {
			sampleSeconds = 60
		}
		storage, err := s.Storage()
		api.OK(w, map[string]interface{}{"points": s.History(rangeName == "24h"), "storage": storage, "storageError": err, "sampleSeconds": sampleSeconds})
	})
}
