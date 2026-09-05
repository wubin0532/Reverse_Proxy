// Package forward 实现 TCP/UDP 四层端口转发。
package forward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/firewall"
	"andey-proxy/internal/guard"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/netutil"
	"andey-proxy/internal/notify"
)

// udpSessionTimeout UDP 会话空闲超时：回包 goroutine 读超时与过期清理共用。
// 定义为包级变量便于测试注入较短值。
var udpSessionTimeout = 90 * time.Second

// Process-wide forwarding budgets, shared by all rules. UDP receive buffers
// alone use up to 16 MiB with this cap. Excess new connections are dropped.
const maxTCPConnections = 256
const maxUDPSessions = 256

// Service 端口转发服务，管理所有规则的监听器。
type Service struct {
	cfg *config.Config

	// FW 可选的防火墙自动放行管理器（main 注入，nil 时跳过）。
	// Start/Reload 后会按 Enabled && AutoFW 的规则上报期望放行集合。
	FW *firewall.Manager

	mu       sync.Mutex
	lmu      sync.Mutex // 保护 logs，与 mu 分离避免 startRuleLocked 持锁时死锁
	wg       sync.WaitGroup
	tcpSlots chan struct{}
	udpSlots chan struct{}
	running  map[string]*ruleRunner // ruleID -> 运行项
	logs     map[string]*RingLog
}

type ruleRunner struct {
	rule   config.ForwardRule
	cancel context.CancelFunc
	done   chan struct{}
}

func NewService(cfg *config.Config) *Service {
	return &Service{tcpSlots: make(chan struct{}, maxTCPConnections), udpSlots: make(chan struct{}, maxUDPSessions), cfg: cfg, running: make(map[string]*ruleRunner), logs: make(map[string]*RingLog)}
}

// Start 启动所有已启用规则（监听失败仅记日志，状态由 RuleStatus 反映）。
func (s *Service) Start() {
	s.cfg.RLock()
	rules := append([]config.ForwardRule(nil), s.cfg.Forwards...)
	s.cfg.RUnlock()
	s.mu.Lock()
	for _, rule := range rules {
		if rule.Enabled {
			_ = s.startRuleLocked(rule)
		}
	}
	s.mu.Unlock()
	// firewall 会执行 uci 外部命令，须在 s.mu 锁外调用，避免阻塞整个 Service 锁
	s.syncFirewall()
}

// Stop 停止全部监听器并等待连接排空。
func (s *Service) Stop() {
	s.mu.Lock()
	var done []chan struct{}
	for id, runner := range s.running {
		runner.cancel()
		done = append(done, runner.done)
		delete(s.running, id)
	}
	s.mu.Unlock()
	for _, ch := range done {
		<-ch
	}
	s.wg.Wait()
	// 服务整体停止：清空 forward 来源的自动放行规则
	if s.FW != nil {
		s.FW.SetDesiredFrom(firewall.SourceForward, nil)
	}
}

// Reload 按当前配置重算运行状态（配置变更后调用）。
// 有规则监听启动失败时返回聚合错误（如端口被占用），调用方可据此回滚配置。
func (s *Service) Reload() error {
	s.cfg.RLock()
	rules := append([]config.ForwardRule(nil), s.cfg.Forwards...)
	s.cfg.RUnlock()
	want := make(map[string]config.ForwardRule)
	for _, rule := range rules {
		if rule.Enabled {
			want[rule.ID] = rule
		}
	}
	var stopped []*ruleRunner
	s.mu.Lock()
	for id, runner := range s.running {
		next, ok := want[id]
		if !ok || !reflect.DeepEqual(runner.rule, next) {
			runner.cancel()
			stopped = append(stopped, runner)
			delete(s.running, id)
		}
	}
	s.mu.Unlock()
	for _, runner := range stopped {
		<-runner.done
	}
	var errs []string
	s.mu.Lock()
	for _, rule := range rules {
		if rule.Enabled {
			if _, ok := s.running[rule.ID]; !ok {
				if err := s.startRuleLocked(rule); err != nil {
					errs = append(errs, fmt.Sprintf("规则 %q: %v", rule.Name, err))
				}
			}
		}
	}
	s.mu.Unlock()
	s.syncFirewall()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// syncFirewall 按当前配置向防火墙管理器上报 forward 来源的期望放行集合：
// Enabled && AutoFW 的规则，从 Listen（如 ":13389"）解析端口，协议按规则取值。
// 解析失败或非 OpenWrt 环境仅记日志，不影响主流程。
// 调用方须在不持有 s.mu 的状态下调用（firewall 会执行 uci 外部命令）。
func (s *Service) syncFirewall() {
	if s.FW == nil {
		return
	}
	var rules []firewall.Rule
	s.cfg.RLock()
	rulesCfg := append([]config.ForwardRule(nil), s.cfg.Forwards...)
	s.cfg.RUnlock()
	for _, rule := range rulesCfg {
		if !rule.Enabled || !rule.AutoFW {
			continue
		}
		port, err := netutil.ListenPort(rule.Listen)
		if err != nil {
			s.logf(rule.ID, "监听地址 %q 端口解析失败，跳过自动放行: %v", rule.Listen, err)
			continue
		}
		proto := strings.ToLower(rule.Proto)
		if proto == "" {
			proto = "tcp"
		}
		rules = append(rules, firewall.Rule{Key: rule.ID, Port: port, Proto: proto})
	}
	s.FW.SetDesiredFrom(firewall.SourceForward, rules)
}

// Logs 返回规则最近日志。
func (s *Service) Logs(ruleID string) []string {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	if rl, ok := s.logs[ruleID]; ok {
		return rl.Entries()
	}
	return nil
}

// RuleStatus reports whether a configured listener is still running.
func (s *Service) RuleStatus(ruleID string) (string, string) {
	s.mu.Lock()
	runner, ok := s.running[ruleID]
	s.mu.Unlock()
	if !ok {
		return "stopped", "监听未启动"
	}
	select {
	case <-runner.done:
		return "error", "监听启动或运行已停止，请查看日志"
	default:
		return "listening", ""
	}
}

func (s *Service) logf(ruleID, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	level := "info"
	if strings.Contains(msg, "失败") || strings.Contains(msg, "错误") {
		level = "error"
	}
	logcenter.Add("forward", ruleID, "", level, msg)
	s.lmu.Lock()
	rl, ok := s.logs[ruleID]
	if !ok {
		rl = NewRingLog(200)
		s.logs[ruleID] = rl
	}
	s.lmu.Unlock()
	rl.Add(msg)
}

// startRuleLocked 启动单条规则：同步建立监听器，失败时返回错误。
// 调用方须持有 s.mu。
func (s *Service) startRuleLocked(rule config.ForwardRule) error {
	ctx, cancel := context.WithCancel(context.Background())
	proto := strings.ToLower(rule.Proto)

	var tcpLn net.Listener
	var udpLn *net.UDPConn
	if proto == "tcp" || proto == "tcpudp" || proto == "" {
		ln, err := net.Listen("tcp", rule.Listen)
		if err != nil {
			cancel()
			s.logf(rule.ID, "TCP 监听 %s 失败: %v", rule.Listen, err)
			notify.Publish(notify.Event{Type: notify.TypeFwdListenError, Entity: rule.Name, Level: notify.LevelError, Message: fmt.Sprintf("转发规则 %s TCP 监听 %s 失败: %v", rule.Name, rule.Listen, err)})
			return fmt.Errorf("TCP 监听 %s 失败: %w", rule.Listen, err)
		}
		tcpLn = ln
	}
	if proto == "udp" || proto == "tcpudp" {
		laddr, err := net.ResolveUDPAddr("udp", rule.Listen)
		if err == nil {
			udpLn, err = net.ListenUDP("udp", laddr)
		}
		if err != nil {
			if tcpLn != nil {
				tcpLn.Close()
			}
			cancel()
			s.logf(rule.ID, "UDP 监听 %s 失败: %v", rule.Listen, err)
			notify.Publish(notify.Event{Type: notify.TypeFwdListenError, Entity: rule.Name, Level: notify.LevelError, Message: fmt.Sprintf("转发规则 %s UDP 监听 %s 失败: %v", rule.Name, rule.Listen, err)})
			return fmt.Errorf("UDP 监听 %s 失败: %w", rule.Listen, err)
		}
	}

	runner := &ruleRunner{rule: rule, cancel: cancel, done: make(chan struct{})}
	s.running[rule.ID] = runner
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(runner.done)
		var wg sync.WaitGroup
		if tcpLn != nil {
			wg.Add(1)
			go func() { defer wg.Done(); s.serveTCP(ctx, rule, tcpLn) }()
		}
		if udpLn != nil {
			wg.Add(1)
			go func() { defer wg.Done(); s.serveUDP(ctx, rule, udpLn) }()
		}
		wg.Wait()
	}()
	s.logf(rule.ID, "规则 %q 已启动，监听 %s", rule.Name, rule.Listen)
	return nil
}

// idleTimeout 返回规则的 TCP 空闲超时，0 或负值取默认 600 秒。
func idleTimeout(rule config.ForwardRule) time.Duration {
	if rule.IdleTimeout > 0 {
		return time.Duration(rule.IdleTimeout) * time.Second
	}
	return 600 * time.Second
}

// idleConn 包装连接读侧：每次 Read 前刷新读 deadline，实现空闲超时。
// 两个方向各自包装，并发 SetReadDeadline 是安全的。
type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleConn) Read(p []byte) (int, error) {
	c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(p)
}

func (s *Service) serveTCP(ctx context.Context, rule config.ForwardRule, ln net.Listener) {
	go func() { <-ctx.Done(); ln.Close() }()
	matcher := guard.CompileIP(rule.IPListMode, rule.IPList)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logf(rule.ID, "Accept 错误: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if ctx.Err() != nil || !reserveSlot(s.tcpSlots) {
			conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.tcpSlots }()
			s.handleTCPConn(ctx, rule, conn, matcher)
		}()
	}
}

func (s *Service) handleTCPConn(ctx context.Context, rule config.ForwardRule, src net.Conn, matcher *guard.IPMatcher) {
	defer src.Close()
	srcIP, _, _ := net.SplitHostPort(src.RemoteAddr().String())
	if !matcher.Allow(srcIP) {
		s.logf(rule.ID, "拒绝来自 %s 的连接（黑白名单）", srcIP)
		return
	}
	target := pickTarget(rule.Targets)
	dst, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", target)
	if err != nil {
		s.logf(rule.ID, "连接目标 %s 失败: %v", target, err)
		return
	}
	defer dst.Close()
	s.logf(rule.ID, "%s -> %s 已建立", src.RemoteAddr(), target)

	timeout := idleTimeout(rule)
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(dst, &idleConn{Conn: src, timeout: timeout})
		// 客户端半关闭（shutdown WR）时向目标传递 EOF，让对端读完请求后完整回写
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		io.Copy(src, &idleConn{Conn: dst, timeout: timeout})
		if tc, ok := src.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	select {
	case <-done:
		// 一侧 copy 结束：等待另一方向完成（半关闭场景），对端不按预期关闭时兜底退出
		select {
		case <-done:
		case <-ctx.Done():
		case <-time.After(30 * time.Second):
		}
	case <-ctx.Done():
	}
}

// udpSession UDP 会话：客户端地址到目标连接的映射。
// lastSeen 用 atomic 读写：主循环在 mu 内写，回包 goroutine 在 mu 外读。
type udpSession struct {
	dst      *net.UDPConn
	lastSeen atomic.Int64  // UnixNano
	done     chan struct{} // 回包 goroutine 退出时关闭
}

func (s *Service) serveUDP(ctx context.Context, rule config.ForwardRule, ln *net.UDPConn) {
	matcher := guard.CompileIP(rule.IPListMode, rule.IPList)
	go func() { <-ctx.Done(); ln.Close() }()

	// 会话表：客户端地址 -> 目标连接
	sessions := make(map[string]*udpSession)
	var mu sync.Mutex

	// 过期清理
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mu.Lock()
				for k, sess := range sessions {
					if time.Since(time.Unix(0, sess.lastSeen.Load())) > udpSessionTimeout {
						// dst 关闭后回包 goroutine 读出错退出并自行 close(done)
						sess.dst.Close()
						delete(sessions, k)
					}
				}
				mu.Unlock()
			}
		}
	}()

	buf := make([]byte, 64*1024)
	for {
		n, srcAddr, err := ln.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				mu.Lock()
				for _, sess := range sessions {
					sess.dst.Close()
				}
				mu.Unlock()
				<-cleanupDone
				return
			}
			continue
		}
		if !matcher.Allow(srcAddr.IP.String()) {
			continue
		}
		key := srcAddr.String()
		mu.Lock()
		sess, ok := sessions[key]
		if ok {
			select {
			case <-sess.done:
				// 回包 goroutine 已退出（目标端长期静默等），重建目标连接
				sess.dst.Close()
				delete(sessions, key)
				ok = false
			default:
			}
		}
		if !ok {
			// Failed reply loops release their socket budget immediately, but their
			// lookup entries may remain until cleanup. Bound those entries as well.
			if len(sessions) >= maxUDPSessions {
				for k, old := range sessions {
					select {
					case <-old.done:
						delete(sessions, k)
					default:
					}
				}
				if len(sessions) >= maxUDPSessions {
					mu.Unlock()
					continue
				}
			}
			if !reserveSlot(s.udpSlots) {
				mu.Unlock()
				continue
			}
			target := pickTarget(rule.Targets)
			taddr, err := net.ResolveUDPAddr("udp", target)
			if err != nil {
				<-s.udpSlots
				mu.Unlock()
				continue
			}
			dst, err := net.DialUDP("udp", nil, taddr)
			if err != nil {
				<-s.udpSlots
				mu.Unlock()
				s.logf(rule.ID, "UDP 连接目标 %s 失败: %v", target, err)
				continue
			}
			sess = &udpSession{dst: dst, done: make(chan struct{})}
			sess.lastSeen.Store(time.Now().UnixNano())
			sessions[key] = sess
			s.logf(rule.ID, "UDP 会话 %s -> %s 已建立", key, target)
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() { <-s.udpSlots }()
				s.udpReplyLoop(ctx, ln, srcAddr, sess)
			}()
		}
		sess.lastSeen.Store(time.Now().UnixNano())
		mu.Unlock()
		sess.dst.Write(buf[:n])
	}
}

// udpReplyLoop 从目标端读回包并回写客户端。
// 读超时不直接退出：客户端仍活跃则续期继续等回包，否则退出并 close(done)，
// 主循环下次命中该会话时会检测 done 并重建。done 仅由此 goroutine 关闭，不会重复 close。
func (s *Service) udpReplyLoop(ctx context.Context, ln *net.UDPConn, srcAddr *net.UDPAddr, sess *udpSession) {
	defer close(sess.done)
	defer sess.dst.Close()
	rbuf := make([]byte, 64*1024)
	for {
		sess.dst.SetReadDeadline(time.Now().Add(udpSessionTimeout))
		n, err := sess.dst.Read(rbuf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() && ctx.Err() == nil {
				if time.Since(time.Unix(0, sess.lastSeen.Load())) < udpSessionTimeout {
					continue
				}
			}
			return
		}
		ln.WriteToUDP(rbuf[:n], srcAddr)
	}
}

// pickTarget 随机取一个目标（简单负载均衡）。
func pickTarget(targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	return targets[time.Now().UnixNano()%int64(len(targets))]
}

func reserveSlot(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}
