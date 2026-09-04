// Package forward 实现 TCP/UDP 四层端口转发。
package forward

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/firewall"
	"andey-proxy/internal/guard"
)

// Service 端口转发服务，管理所有规则的监听器。
type Service struct {
	cfg *config.Config

	// FW 可选的防火墙自动放行管理器（main 注入，nil 时跳过）。
	// Start/Reload 后会按 Enabled && AutoFW 的规则上报期望放行集合。
	FW *firewall.Manager

	mu      sync.Mutex
	lmu     sync.Mutex // 保护 logs，与 mu 分离避免 startRuleLocked 持锁时死锁
	wg      sync.WaitGroup
	running map[string]context.CancelFunc // ruleID -> 停止函数
	logs    map[string]*RingLog
}

func NewService(cfg *config.Config) *Service {
	return &Service{cfg: cfg, running: make(map[string]context.CancelFunc), logs: make(map[string]*RingLog)}
}

// Start 启动所有已启用规则。
func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.cfg.Forwards {
		if rule.Enabled {
			s.startRuleLocked(rule)
		}
	}
	s.syncFirewall()
}

// Stop 停止全部监听器并等待连接排空。
func (s *Service) Stop() {
	s.mu.Lock()
	for id, cancel := range s.running {
		cancel()
		delete(s.running, id)
	}
	s.mu.Unlock()
	s.wg.Wait()
	// 服务整体停止：清空 forward 来源的自动放行规则
	if s.FW != nil {
		s.FW.SetDesiredFrom(firewall.SourceForward, nil)
	}
}

// Reload 按当前配置重算运行状态（配置变更后调用）。
func (s *Service) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := make(map[string]bool)
	for _, rule := range s.cfg.Forwards {
		want[rule.ID] = rule.Enabled
	}
	for id, cancel := range s.running {
		if !want[id] {
			cancel()
			delete(s.running, id)
		}
	}
	for _, rule := range s.cfg.Forwards {
		if rule.Enabled {
			if _, ok := s.running[rule.ID]; !ok {
				s.startRuleLocked(rule)
			}
		}
	}
	s.syncFirewall()
}

// syncFirewall 按当前配置向防火墙管理器上报 forward 来源的期望放行集合：
// Enabled && AutoFW 的规则，从 Listen（如 ":13389"）解析端口，协议按规则取值。
// 解析失败或非 OpenWrt 环境仅记日志，不影响主流程。调用方须持有 s.mu。
func (s *Service) syncFirewall() {
	if s.FW == nil {
		return
	}
	var rules []firewall.Rule
	for _, rule := range s.cfg.Forwards {
		if !rule.Enabled || !rule.AutoFW {
			continue
		}
		port, err := listenPort(rule.Listen)
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

// listenPort 从监听地址（":13389"、"0.0.0.0:13389" 或裸 "13389"）解析端口号。
func listenPort(listen string) (int, error) {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		p = strings.TrimPrefix(listen, ":")
	}
	return strconv.Atoi(p)
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

func (s *Service) logf(ruleID, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[转发] %s", msg)
	s.lmu.Lock()
	rl, ok := s.logs[ruleID]
	if !ok {
		rl = NewRingLog(200)
		s.logs[ruleID] = rl
	}
	s.lmu.Unlock()
	rl.Add(msg)
}

func (s *Service) startRuleLocked(rule config.ForwardRule) {
	ctx, cancel := context.WithCancel(context.Background())
	s.running[rule.ID] = cancel
	proto := strings.ToLower(rule.Proto)
	if proto == "tcp" || proto == "tcpudp" || proto == "" {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.serveTCP(ctx, rule) }()
	}
	if proto == "udp" || proto == "tcpudp" {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.serveUDP(ctx, rule) }()
	}
	s.logf(rule.ID, "规则 %q 已启动，监听 %s", rule.Name, rule.Listen)
}

func (s *Service) serveTCP(ctx context.Context, rule config.ForwardRule) {
	ln, err := net.Listen("tcp", rule.Listen)
	if err != nil {
		s.logf(rule.ID, "TCP 监听 %s 失败: %v", rule.Listen, err)
		return
	}
	go func() { <-ctx.Done(); ln.Close() }()
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
		go s.handleTCPConn(ctx, rule, conn)
	}
}

func (s *Service) handleTCPConn(ctx context.Context, rule config.ForwardRule, src net.Conn) {
	defer src.Close()
	srcIP, _, _ := net.SplitHostPort(src.RemoteAddr().String())
	if !guard.AllowIP(rule.IPListMode, rule.IPList, srcIP) {
		s.logf(rule.ID, "拒绝来自 %s 的连接（黑白名单）", srcIP)
		return
	}
	target := pickTarget(rule.Targets)
	dst, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		s.logf(rule.ID, "连接目标 %s 失败: %v", target, err)
		return
	}
	defer dst.Close()
	s.logf(rule.ID, "%s -> %s 已建立", src.RemoteAddr(), target)

	done := make(chan struct{}, 2)
	go func() { io.Copy(dst, src); done <- struct{}{} }()
	go func() { io.Copy(src, dst); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *Service) serveUDP(ctx context.Context, rule config.ForwardRule) {
	laddr, err := net.ResolveUDPAddr("udp", rule.Listen)
	if err != nil {
		s.logf(rule.ID, "UDP 地址解析失败: %v", err)
		return
	}
	ln, err := net.ListenUDP("udp", laddr)
	if err != nil {
		s.logf(rule.ID, "UDP 监听 %s 失败: %v", rule.Listen, err)
		return
	}
	go func() { <-ctx.Done(); ln.Close() }()

	// 会话表：客户端地址 -> 目标连接
	type session struct {
		dst      *net.UDPConn
		lastSeen time.Time
	}
	sessions := make(map[string]*session)
	var mu sync.Mutex

	// 过期清理
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mu.Lock()
				for k, sess := range sessions {
					if time.Since(sess.lastSeen) > 90*time.Second {
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
				return
			}
			continue
		}
		if !guard.AllowIP(rule.IPListMode, rule.IPList, srcAddr.IP.String()) {
			continue
		}
		key := srcAddr.String()
		mu.Lock()
		sess, ok := sessions[key]
		if !ok {
			target := pickTarget(rule.Targets)
			taddr, err := net.ResolveUDPAddr("udp", target)
			if err != nil {
				mu.Unlock()
				continue
			}
			dst, err := net.DialUDP("udp", nil, taddr)
			if err != nil {
				mu.Unlock()
				s.logf(rule.ID, "UDP 连接目标 %s 失败: %v", target, err)
				continue
			}
			sess = &session{dst: dst, lastSeen: time.Now()}
			sessions[key] = sess
			s.logf(rule.ID, "UDP 会话 %s -> %s 已建立", key, target)
			go func(srcAddr *net.UDPAddr, sess *session) {
				rbuf := make([]byte, 64*1024)
				for {
					sess.dst.SetReadDeadline(time.Now().Add(90 * time.Second))
					n, err := sess.dst.Read(rbuf)
					if err != nil {
						return
					}
					ln.WriteToUDP(rbuf[:n], srcAddr)
				}
			}(srcAddr, sess)
		}
		sess.lastSeen = time.Now()
		mu.Unlock()
		sess.dst.Write(buf[:n])
	}
}

// pickTarget 随机取一个目标（简单负载均衡）。
func pickTarget(targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	return targets[time.Now().UnixNano()%int64(len(targets))]
}
