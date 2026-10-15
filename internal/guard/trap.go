package guard

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"andey-proxy/internal/config"
)

type TrapStatus struct {
	Config    config.TCPTrap `json:"config"`
	Running   bool           `json:"running"`
	Listening []string       `json:"listening"`
	Error     string         `json:"error,omitempty"`
	Captured  uint64         `json:"captured"`
	Exempted  uint64         `json:"exempted"`
}

// No payload reads or per-connection goroutines: completing a TCP handshake is
// sufficient. Listener count, bans and events all have hard bounds.
type TrapManager struct {
	control            sync.Mutex
	mu                 sync.Mutex
	wg                 sync.WaitGroup
	cfg                *config.Config
	engine             *Engine
	conf               config.TCPTrap
	exempt             *IPMatcher
	listeners          map[string]net.Listener
	lastError          string
	captured, exempted uint64
	AdminPort          int // Actual CLI port, which may override the saved settings.
}

func newTrapManager(c *config.Config, e *Engine) *TrapManager {
	return &TrapManager{cfg: c, engine: e, listeners: map[string]net.Listener{}}
}

func cloneTrap(t config.TCPTrap) config.TCPTrap {
	t.Ports = append([]int{}, t.Ports...)
	t.AllowIPs = append([]string{}, t.AllowIPs...)
	return t
}

// Apply stages all new sockets first. A bind or disk error leaves old config and
// listeners intact; reused sockets keep working across configuration changes.
func (m *TrapManager) Apply(conf config.TCPTrap, persist bool) error {
	m.control.Lock()
	defer m.control.Unlock()
	return m.apply(conf, persist)
}
func (m *TrapManager) apply(conf config.TCPTrap, persist bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	conf = cloneTrap(conf)
	m.cfg.RLock()
	err := m.cfg.ValidateTrap(&conf)
	m.cfg.RUnlock()
	if err != nil {
		return err
	}
	if conf.Enabled {
		for _, p := range conf.Ports {
			if p == m.AdminPort && p != 0 {
				return fmt.Errorf("诱捕端口不能占用实际管理端口")
			}
		}
	}
	next := map[string]net.Listener{}
	staged := map[string]net.Listener{}
	closeStaged := func() {
		for _, ln := range staged {
			_ = ln.Close()
		}
	}
	if conf.Enabled {
		for _, p := range conf.Ports {
			addr := net.JoinHostPort(conf.ListenHost, strconv.Itoa(p))
			if ln := m.listeners[addr]; ln != nil {
				next[addr] = ln
				continue
			}
			network := "tcp4"
			if net.ParseIP(conf.ListenHost).To4() == nil {
				network = "tcp6"
			}
			ln, err := net.Listen(network, addr)
			if err != nil {
				closeStaged()
				return fmt.Errorf("诱捕监听%s失败：端口被占用或地址不可用", addr)
			}
			next[addr], staged[addr] = ln, ln
		}
	}
	if persist {
		if err := m.cfg.Update(func(c *config.Config) error { saved := cloneTrap(conf); c.TCPTrap = &saved; return nil }); err != nil {
			closeStaged()
			return err
		}
	}
	for addr, ln := range m.listeners {
		if next[addr] != ln {
			_ = ln.Close()
		}
	}
	m.listeners, m.conf, m.exempt, m.lastError = next, conf, CompileIP("whitelist", conf.AllowIPs), ""
	for addr, ln := range staged {
		m.wg.Add(1)
		go m.accept(addr, ln)
	}
	return nil
}

func (m *TrapManager) Reload() error {
	m.control.Lock()
	defer m.control.Unlock()
	m.cfg.RLock()
	conf := config.TCPTrap{}
	if m.cfg.TCPTrap != nil {
		conf = cloneTrap(*m.cfg.TCPTrap)
	}
	m.cfg.RUnlock()
	if err := m.apply(conf, false); err != nil {
		// Imports/startup must never silently leave listeners for an old config.
		m.stop()
		m.mu.Lock()
		m.conf, m.lastError = conf, err.Error()
		m.mu.Unlock()
		return err
	}
	return nil
}

func (m *TrapManager) Stop() {
	m.control.Lock()
	defer m.control.Unlock()
	m.stop()
}
func (m *TrapManager) stop() {
	m.mu.Lock()
	for _, ln := range m.listeners {
		_ = ln.Close()
	}
	m.listeners = map[string]net.Listener{}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *TrapManager) accept(addr string, ln net.Listener) {
	defer m.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			m.mu.Lock()
			if m.listeners[addr] == ln {
				delete(m.listeners, addr)
				m.lastError = "诱捕监听" + addr + "已停止"
			}
			m.mu.Unlock()
			return
		}
		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		_ = conn.Close()
		if err != nil {
			continue
		}
		m.mu.Lock()
		if m.listeners[addr] == ln {
			m.captureLocked(host, addr)
		}
		m.mu.Unlock()
	}
}

func (m *TrapManager) captureLocked(host, addr string) {
	ip := net.ParseIP(host)
	if ip == nil {
		return
	}
	host = ip.String()
	if ip.IsLoopback() || ip.IsUnspecified() || m.exempt != nil && m.exempt.Allow(host) {
		m.exempted++
		return
	}
	b := Ban{IP: host, Reason: "TCP端口诱捕 " + addr, ExpiresAt: time.Now().UTC().Add(time.Duration(m.conf.BanSeconds) * time.Second)}
	if err := m.engine.AddBan(b); err != nil {
		m.lastError = err.Error()
		return
	}
	m.captured++
	m.engine.mu.Lock()
	m.engine.nextID++
	ev := Event{ID: m.engine.nextID, Time: time.Now().UTC(), Source: "trap", EntityID: addr, IP: host, Reason: b.Reason, Action: "ban"}
	m.engine.events.append(ev, MaxEvents)
	m.engine.mu.Unlock()
}

func (m *TrapManager) Status() TrapStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := []string{}
	for addr := range m.listeners {
		list = append(list, addr)
	}
	sort.Strings(list)
	return TrapStatus{Config: cloneTrap(m.conf), Running: m.conf.Enabled && len(list) > 0 && len(list) == len(m.conf.Ports), Listening: list, Error: m.lastError, Captured: m.captured, Exempted: m.exempted}
}
