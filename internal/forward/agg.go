package forward

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
)

// 连接日志聚合：端口扫描会产生海量逐连接日志，淹没环形日志与日志中心。
// 改为按规则按分钟汇总为一条（连接数、字节数、去重对端数、拒绝/失败数），
// 每个新对端仍即时记一条，保证环形日志实时可用。
// 包级变量便于测试注入较短窗口。
var connSummaryInterval = time.Minute

const (
	maxKnownPeersPerRule = 4096 // 每条规则记住的“已知对端”上限，防扫描耗尽内存
	maxWindowPeers       = 4096 // 单个汇总窗口内去重对端数上限
)

type connAgg struct {
	mu             sync.Mutex
	windowStart    time.Time
	tcpConns       int
	udpSessions    int
	bytesUp        int64
	bytesDown      int64
	peers          map[string]struct{}
	peersFull      bool
	rejected       int
	dialFailed     int
	rejectSeen     bool // 本窗口已即时记录过拒绝
	dialSeen       bool // 本窗口已即时记录过目标连接失败
	known          map[string]struct{}
	overflowLogged bool // 已见对端表打满提示只记一次
}

func newConnAgg() *connAgg {
	return &connAgg{peers: make(map[string]struct{}), known: make(map[string]struct{})}
}

func (s *Service) aggFor(ruleID string) *connAgg {
	s.amu.Lock()
	defer s.amu.Unlock()
	a, ok := s.aggs[ruleID]
	if !ok {
		a = newConnAgg()
		s.aggs[ruleID] = a
	}
	return a
}

// maybeFlushLocked 窗口到期且有活动时生成汇总并重置窗口（调用方须持有 a.mu）。
func (a *connAgg) maybeFlushLocked(now time.Time) string {
	if a.windowStart.IsZero() {
		a.windowStart = now
		return ""
	}
	if now.Sub(a.windowStart) < connSummaryInterval {
		return ""
	}
	msg := a.summaryLocked()
	a.windowStart = now
	a.tcpConns, a.udpSessions = 0, 0
	a.bytesUp, a.bytesDown = 0, 0
	a.peers = make(map[string]struct{})
	a.peersFull = false
	a.rejected, a.dialFailed = 0, 0
	a.rejectSeen, a.dialSeen = false, false
	return msg
}

func (a *connAgg) summaryLocked() string {
	if a.tcpConns == 0 && a.udpSessions == 0 && a.rejected == 0 && a.dialFailed == 0 {
		return ""
	}
	var parts []string
	if a.tcpConns > 0 {
		parts = append(parts, fmt.Sprintf("%d 个 TCP 连接", a.tcpConns))
	}
	if a.udpSessions > 0 {
		parts = append(parts, fmt.Sprintf("%d 个 UDP 会话", a.udpSessions))
	}
	if len(a.peers) > 0 {
		suffix := ""
		if a.peersFull {
			suffix = "+"
		}
		parts = append(parts, fmt.Sprintf("独立对端 %d%s", len(a.peers), suffix))
	}
	if a.bytesUp > 0 || a.bytesDown > 0 {
		parts = append(parts, fmt.Sprintf("上行 %s，下行 %s", humanBytes(a.bytesUp), humanBytes(a.bytesDown)))
	}
	if a.rejected > 0 {
		parts = append(parts, fmt.Sprintf("拒绝 %d", a.rejected))
	}
	if a.dialFailed > 0 {
		parts = append(parts, fmt.Sprintf("目标连接失败 %d", a.dialFailed))
	}
	return "最近一分钟: " + strings.Join(parts, "，")
}

// trackPeerLocked 记录对端。首次见到的对端 newPeer=true；表已满而丢弃的新对端 overflow=true。
func (a *connAgg) trackPeerLocked(peer string) (newPeer, overflow bool) {
	if peer == "" {
		return false, false
	}
	if len(a.peers) < maxWindowPeers {
		a.peers[peer] = struct{}{}
	} else {
		a.peersFull = true
	}
	if _, ok := a.known[peer]; ok {
		return false, false
	}
	if len(a.known) >= maxKnownPeersPerRule {
		return false, true
	}
	a.known[peer] = struct{}{}
	return true, false
}

// noteConn 记录一条已建立的 TCP 连接或 UDP 会话。
func (s *Service) noteConn(rule config.ForwardRule, peer, target string, udp bool) {
	a := s.aggFor(rule.ID)
	a.mu.Lock()
	summary := a.maybeFlushLocked(time.Now())
	if udp {
		a.udpSessions++
	} else {
		a.tcpConns++
	}
	newPeer, overflow := a.trackPeerLocked(peer)
	notice := overflow && !a.overflowLogged
	if notice {
		a.overflowLogged = true
	}
	a.mu.Unlock()
	if newPeer {
		proto := "TCP"
		if udp {
			proto = "UDP"
		}
		s.logf(rule.ID, "新对端 %s %s 已连接 -> %s", peer, proto, target)
	}
	if notice {
		s.logf(rule.ID, "已知对端数已达上限 %d，后续新对端只计入每分钟汇总", maxKnownPeersPerRule)
	}
	if summary != "" {
		s.logf(rule.ID, "%s", summary)
	}
}

// noteBytes 累加双向字节数。
func (s *Service) noteBytes(ruleID string, up, down int64) {
	a := s.aggFor(ruleID)
	a.mu.Lock()
	summary := a.maybeFlushLocked(time.Now())
	a.bytesUp += up
	a.bytesDown += down
	a.mu.Unlock()
	if summary != "" {
		s.logf(ruleID, "%s", summary)
	}
}

// noteReject 记录被黑白名单拒绝的连接：每窗口第一条即时记，其余只计数。
func (s *Service) noteReject(rule config.ForwardRule, srcIP string) {
	a := s.aggFor(rule.ID)
	a.mu.Lock()
	summary := a.maybeFlushLocked(time.Now())
	a.rejected++
	a.trackPeerLocked(srcIP)
	first := !a.rejectSeen
	a.rejectSeen = true
	a.mu.Unlock()
	if first {
		s.logf(rule.ID, "拒绝来自 %s 的连接（黑白名单）", srcIP)
	}
	if summary != "" {
		s.logf(rule.ID, "%s", summary)
	}
}

// noteDialFail 记录目标连接失败：每窗口第一条即时记，其余只计数。
func (s *Service) noteDialFail(rule config.ForwardRule, target string, err error) {
	a := s.aggFor(rule.ID)
	a.mu.Lock()
	summary := a.maybeFlushLocked(time.Now())
	a.dialFailed++
	first := !a.dialSeen
	a.dialSeen = true
	a.mu.Unlock()
	if first {
		s.logf(rule.ID, "连接目标 %s 失败: %v", target, err)
	}
	if summary != "" {
		s.logf(rule.ID, "%s", summary)
	}
}

// flushConnSummaries 强制落盘所有规则的未汇总窗口（服务停止时调用）。
func (s *Service) flushConnSummaries() {
	s.amu.Lock()
	aggs := make(map[string]*connAgg, len(s.aggs))
	for id, a := range s.aggs {
		aggs[id] = a
	}
	s.amu.Unlock()
	for id, a := range aggs {
		a.mu.Lock()
		msg := a.summaryLocked()
		a.windowStart = time.Now()
		a.tcpConns, a.udpSessions = 0, 0
		a.bytesUp, a.bytesDown = 0, 0
		a.peers = make(map[string]struct{})
		a.peersFull = false
		a.rejected, a.dialFailed = 0, 0
		a.rejectSeen, a.dialSeen = false, false
		a.mu.Unlock()
		if msg != "" {
			s.logf(id, "%s", msg)
		}
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
