package webproxy

import (
	"sync/atomic"
)

// siteStats 站点级流量统计，全部字段用 atomic 保证并发安全。
// 挂在 Service 上按站点 ID 聚合：热重载重建 handler 缓存不会丢失；
// 不持久化，进程重启后清零。
type siteStats struct {
	requests  atomic.Int64
	bytesIn   atomic.Int64 // 请求体字节数（ContentLength，未知记 0）
	bytesOut  atomic.Int64 // 响应写出字节数（statusWriter 统计）
	status1xx atomic.Int64
	status2xx atomic.Int64
	status3xx atomic.Int64
	status4xx atomic.Int64
	status5xx atomic.Int64
}

// add 累加一次请求的统计。
func (st *siteStats) add(status int, bytesIn, bytesOut int64) {
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
		// 5xx 及其他异常状态码并入 5xx 桶
		st.status5xx.Add(1)
	}
}

// SiteStats 站点流量统计快照（对外只读视图）。
type SiteStats struct {
	Requests  int64 `json:"requests"`
	BytesIn   int64 `json:"bytesIn"`
	BytesOut  int64 `json:"bytesOut"`
	Status1xx int64 `json:"status1xx"`
	Status2xx int64 `json:"status2xx"`
	Status3xx int64 `json:"status3xx"`
	Status4xx int64 `json:"status4xx"`
	Status5xx int64 `json:"status5xx"`
}

// snapshot 读取当前统计快照。
func (st *siteStats) snapshot() SiteStats {
	return SiteStats{
		Requests:  st.requests.Load(),
		BytesIn:   st.bytesIn.Load(),
		BytesOut:  st.bytesOut.Load(),
		Status1xx: st.status1xx.Load(),
		Status2xx: st.status2xx.Load(),
		Status3xx: st.status3xx.Load(),
		Status4xx: st.status4xx.Load(),
		Status5xx: st.status5xx.Load(),
	}
}

// statsFor 返回站点对应的统计桶（不存在则创建），供站点请求路径埋点使用。
func (s *Service) statsFor(siteID string) *siteStats {
	v, _ := s.stats.LoadOrStore(siteID, &siteStats{})
	return v.(*siteStats)
}

// deleteStats 站点删除/禁用时清理其统计。
func (s *Service) deleteStats(siteID string) {
	s.stats.Delete(siteID)
}

// AllSiteStats 返回全部站点统计快照（siteID → 统计）。无数据时返回空 map。
func (s *Service) AllSiteStats() map[string]SiteStats {
	out := make(map[string]SiteStats)
	s.stats.Range(func(key, value any) bool {
		out[key.(string)] = value.(*siteStats).snapshot()
		return true
	})
	return out
}
