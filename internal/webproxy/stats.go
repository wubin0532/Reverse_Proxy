package webproxy

import (
	"sync"
	"sync/atomic"

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

type siteStats struct {
	total trafficStats
	rules sync.Map // ruleID -> *trafficStats
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
