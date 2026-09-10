package webproxy

import (
	"sync"
	"sync/atomic"
	"time"

	"andey-proxy/internal/config"
)

// trafficStats 保存一组并发安全的内存统计。统计不持久化，进程重启后清零。
type trafficStats struct {
	requests  atomic.Int64
	bytesIn   atomic.Int64
	bytesOut  atomic.Int64
	active    atomic.Int64
	status1xx atomic.Int64
	status2xx atomic.Int64
	status3xx atomic.Int64
	status4xx atomic.Int64
	status5xx atomic.Int64
}

func (st *trafficStats) begin() { st.active.Add(1) }

func (st *trafficStats) finish(status int, bytesIn, bytesOut int64) {
	st.active.Add(-1)
	st.requests.Add(1)
	st.bytesIn.Add(bytesIn)
	st.bytesOut.Add(bytesOut)
	switch {
	case status >= 100 && status < 200:
		st.status1xx.Add(1)
	case status >= 200 && status < 300:
		st.status2xx.Add(1)
	case status >= 300 && status < 400:
		st.status3xx.Add(1)
	case status >= 400 && status < 500:
		st.status4xx.Add(1)
	default:
		st.status5xx.Add(1)
	}
}

// StatsSnapshot 是站点或子规则统计的只读快照。
type StatsSnapshot struct {
	Requests  int64 `json:"requests"`
	BytesIn   int64 `json:"bytesIn"`
	BytesOut  int64 `json:"bytesOut"`
	Active    int64 `json:"active"`
	Status1xx int64 `json:"status1xx"`
	Status2xx int64 `json:"status2xx"`
	Status3xx int64 `json:"status3xx"`
	Status4xx int64 `json:"status4xx"`
	Status5xx int64 `json:"status5xx"`
	// Backends 由 API 层附带（reverse 规则的后端健康状态），统计桶本身不填。
	Backends []BackendHealth `json:"backends,omitempty"`
}

func (st *trafficStats) snapshot() StatsSnapshot {
	return StatsSnapshot{
		Requests: st.requests.Load(), BytesIn: st.bytesIn.Load(), BytesOut: st.bytesOut.Load(), Active: st.active.Load(),
		Status1xx: st.status1xx.Load(), Status2xx: st.status2xx.Load(), Status3xx: st.status3xx.Load(),
		Status4xx: st.status4xx.Load(), Status5xx: st.status5xx.Load(),
	}
}

// SiteStats 包含站点总量和按规则 ID 分组的统计。
type SiteStats struct {
	StatsSnapshot
	Rules map[string]StatsSnapshot `json:"rules"`
}

// seriesMaxPoints 每站点最多保留的分钟桶数（24h）。
const seriesMaxPoints = 1440

// minuteBucket 当前分钟的累加桶（原子计数，无锁快速路径）。
type minuteBucket struct {
	minute   int64
	requests atomic.Int64
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
}

// seriesPoint 已完成分钟的快照。
type seriesPoint struct {
	minute   int64
	requests int64
	bytesIn  int64
	bytesOut int64
}

// siteSeries 站点分钟级流量环形缓冲：当前桶用原子指针 + 原子加，
// 分钟切换时加锁封存旧桶，超限丢弃最旧。内存约 1440×32B/站点，随站点统计一并清理。
type siteSeries struct {
	mu     sync.Mutex
	cur    atomic.Pointer[minuteBucket]
	points []seriesPoint // 已完成分钟桶，最旧在前
}

func (s *siteSeries) add(now time.Time, bytesIn, bytesOut int64) {
	m := now.Unix() / 60
	if b := s.cur.Load(); b != nil && b.minute == m {
		b.requests.Add(1)
		b.bytesIn.Add(bytesIn)
		b.bytesOut.Add(bytesOut)
		return
	}
	s.mu.Lock()
	b := s.cur.Load()
	if b == nil || b.minute != m {
		if b != nil {
			s.points = append(s.points, seriesPoint{b.minute, b.requests.Load(), b.bytesIn.Load(), b.bytesOut.Load()})
			if len(s.points) > seriesMaxPoints {
				s.points = append([]seriesPoint(nil), s.points[len(s.points)-seriesMaxPoints:]...)
			}
		}
		nb := &minuteBucket{minute: m}
		s.cur.Store(nb)
		b = nb
	}
	s.mu.Unlock()
	b.requests.Add(1)
	b.bytesIn.Add(bytesIn)
	b.bytesOut.Add(bytesOut)
}

// snapshot 返回 [from, to]（分钟 Unix 戳）的稠密序列，无流量的分钟补零。
func (s *siteSeries) snapshot(from, to int64) []seriesPoint {
	s.mu.Lock()
	points := append([]seriesPoint(nil), s.points...)
	cur := s.cur.Load()
	s.mu.Unlock()
	if cur != nil && cur.minute >= from {
		points = append(points, seriesPoint{cur.minute, cur.requests.Load(), cur.bytesIn.Load(), cur.bytesOut.Load()})
	}
	byMinute := make(map[int64]seriesPoint, len(points))
	for _, p := range points {
		byMinute[p.minute] = p
	}
	out := make([]seriesPoint, 0, to-from+1)
	for m := from; m <= to; m++ {
		if p, ok := byMinute[m]; ok {
			out = append(out, p)
		} else {
			out = append(out, seriesPoint{minute: m})
		}
	}
	return out
}

type siteStats struct {
	total  trafficStats
	rules  sync.Map // ruleID -> *trafficStats
	series siteSeries
}

func (st *siteStats) ruleFor(ruleID string) *trafficStats {
	v, _ := st.rules.LoadOrStore(ruleID, &trafficStats{})
	return v.(*trafficStats)
}

func (st *siteStats) beginSite() { st.total.begin() }

func (st *siteStats) beginRule(ruleID string) {
	if ruleID != "" {
		st.ruleFor(ruleID).begin()
	}
}

func (st *siteStats) finish(ruleID string, status int, bytesIn, bytesOut int64) {
	st.total.finish(status, bytesIn, bytesOut)
	st.series.add(time.Now(), bytesIn, bytesOut)
	if ruleID != "" {
		st.ruleFor(ruleID).finish(status, bytesIn, bytesOut)
	}
}

func (st *siteStats) snapshot() SiteStats {
	out := SiteStats{StatsSnapshot: st.total.snapshot(), Rules: make(map[string]StatsSnapshot)}
	st.rules.Range(func(key, value any) bool {
		out.Rules[key.(string)] = value.(*trafficStats).snapshot()
		return true
	})
	return out
}

func (st *siteStats) pruneRules(rules []config.SubRule) {
	keep := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		keep[rule.ID] = struct{}{}
	}
	st.rules.Range(func(key, _ any) bool {
		if _, ok := keep[key.(string)]; !ok {
			st.rules.Delete(key)
		}
		return true
	})
}

func (s *Service) statsFor(siteID string) *siteStats {
	v, _ := s.stats.LoadOrStore(siteID, &siteStats{})
	return v.(*siteStats)
}

func (s *Service) deleteStats(siteID string) { s.stats.Delete(siteID) }

// AllSiteStats 返回全部站点统计快照（siteID → 统计）。
func (s *Service) AllSiteStats() map[string]SiteStats {
	out := make(map[string]SiteStats)
	s.stats.Range(func(key, value any) bool {
		out[key.(string)] = value.(*siteStats).snapshot()
		return true
	})
	return out
}

// SiteSeriesView 站点分钟级流量时间序列（API 响应）。
// Points 为紧凑数组 [分钟Unix戳, 请求数, 入字节, 出字节]，无流量的分钟补零。
type SiteSeriesView struct {
	SiteID string    `json:"siteId"`
	Step   int64     `json:"step"` // 恒为 60（秒）
	From   int64     `json:"from"` // 首点分钟 Unix 戳
	To     int64     `json:"to"`   // 末点分钟 Unix 戳（含当前进行中的分钟）
	Points [][]int64 `json:"points"`
}

// SiteSeries 返回站点最近 span 的分钟级流量序列；无统计数据的站点返回零填充序列。
func (s *Service) SiteSeries(siteID string, span time.Duration) SiteSeriesView {
	to := time.Now().Unix() / 60
	from := to - int64(span/time.Minute) + 1
	view := SiteSeriesView{SiteID: siteID, Step: 60, From: from * 60, To: to * 60, Points: [][]int64{}}
	v, ok := s.stats.Load(siteID)
	if !ok {
		for m := from; m <= to; m++ {
			view.Points = append(view.Points, []int64{m * 60, 0, 0, 0})
		}
		return view
	}
	for _, p := range v.(*siteStats).series.snapshot(from, to) {
		view.Points = append(view.Points, []int64{p.minute * 60, p.requests, p.bytesIn, p.bytesOut})
	}
	return view
}
