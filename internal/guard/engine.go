package guard

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"andey-proxy/internal/config"
)

const MaxBans = 2048
const MaxEvents = 512

type Ban struct {
	IP        string    `json:"ip"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type Event struct {
	ID       uint64    `json:"id"`
	Time     time.Time `json:"time"`
	Source   string    `json:"source"`
	EntityID string    `json:"entityId"`
	IP       string    `json:"ip"`
	PolicyID string    `json:"policyId,omitempty"`
	Reason   string    `json:"reason"`
	Action   string    `json:"action"`
}

// eventRing 固定容量事件环形缓冲。旧实现在每次拒绝时 copy 最多 512 条事件，
// 且全程持 e.mu；被封禁的 IP 持续发包（UDP 转发按包调用）会把单包成本放大成
// 一次数百项的搬移，这里改为原地覆盖、零搬移。
type eventRing struct {
	buf   []Event
	start int
}

func (r *eventRing) append(e Event, limit int) {
	if limit <= 0 {
		return
	}
	if len(r.buf) < limit {
		r.buf = append(r.buf, e)
		return
	}
	r.buf[r.start] = e
	r.start++
	if r.start == len(r.buf) {
		r.start = 0
	}
}

func (r *eventRing) len() int { return len(r.buf) }

// newestFirst 返回新到旧的副本（API 展示顺序）。
func (r *eventRing) newestFirst() []Event {
	n := len(r.buf)
	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, r.buf[(r.start+n-1-i)%n])
	}
	return out
}

type Summary struct {
	Policies       int    `json:"policies"`
	GlobalPolicies int    `json:"globalPolicies"`
	ActiveBans     int    `json:"activeBans"`
	Rejected       uint64 `json:"rejected"`
	Events         int    `json:"events"`
}
type compiledPolicy struct {
	conf config.ProtectionPolicy
	ip   *IPMatcher
}
type policySnapshot struct {
	policies map[string]compiledPolicy
	globals  []compiledPolicy
}

// Engine publishes immutable policy snapshots. Bans and events are bounded,
// process-local runtime data and are never written to the encrypted config.
type Engine struct {
	Trap     *TrapManager
	cfg      *config.Config
	snapshot atomic.Pointer[policySnapshot]
	mu       sync.Mutex
	bans     map[string]Ban
	events   eventRing
	rejected uint64
	nextID   uint64
	mutation sync.Mutex
	access   accessRuntime
	cc       ccRuntime
}

func NewEngine(cfg *config.Config) *Engine {
	e := &Engine{cfg: cfg, bans: make(map[string]Ban)}
	e.snapshot.Store(&policySnapshot{policies: map[string]compiledPolicy{}})
	_ = e.Reload()
	e.Trap = newTrapManager(cfg, e)
	return e
}
func ValidatePolicy(p *config.ProtectionPolicy) error {
	if err := p.ValidateExtensions(); err != nil {
		return err
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 128 {
		return fmt.Errorf("策略名称不能为空且最多128字节")
	}
	for _, mode := range []string{p.IPListMode, p.UAListMode} {
		if mode != "" && mode != "off" && mode != "whitelist" && mode != "blacklist" {
			return fmt.Errorf("名单模式无效")
		}
	}
	if len(p.IPList) > 4096 || len(p.UAList) > 256 {
		return fmt.Errorf("策略名单超过容量限制")
	}
	for i, v := range p.IPList {
		v = strings.TrimSpace(v)
		if _, _, err := net.ParseCIDR(v); err != nil && net.ParseIP(v) == nil {
			return fmt.Errorf("IP或CIDR无效: %s", v)
		}
		p.IPList[i] = v
	}
	for i, v := range p.UAList {
		v = strings.TrimSpace(v)
		if v == "" || len(v) > 256 {
			return fmt.Errorf("UA关键词不能为空且最多256字节")
		}
		p.UAList[i] = v
	}
	if p.IPListMode == "whitelist" && len(p.IPList) == 0 {
		return fmt.Errorf("IP白名单不能为空")
	}
	if p.UAListMode == "whitelist" && len(p.UAList) == 0 {
		return fmt.Errorf("UA白名单不能为空")
	}
	return nil
}

// ValidateReferenceLocked must be called while the caller holds Config's lock.
func ValidateReferenceLocked(c *config.Config, id string) error {
	if id == "" {
		return nil
	}
	for _, p := range c.ProtectionPolicies {
		if p.ID == id {
			return nil
		}
	}
	return fmt.Errorf("引用的防护策略不存在")
}
func (e *Engine) Reload() error {
	e.cfg.RLock()
	policies := make([]config.ProtectionPolicy, len(e.cfg.ProtectionPolicies))
	for i, p := range e.cfg.ProtectionPolicies {
		policies[i] = p
		if p.WAF != nil {
			v := *p.WAF
			policies[i].WAF = &v
		}
		if p.CC != nil {
			v := *p.CC
			policies[i].CC = &v
		}
		policies[i].IPList = append([]string(nil), p.IPList...)
		policies[i].UAList = append([]string(nil), p.UAList...)
	}
	e.cfg.RUnlock()
	next := &policySnapshot{policies: make(map[string]compiledPolicy)}
	for _, p := range policies {
		if p.ID == "" {
			return fmt.Errorf("策略ID不能为空")
		}
		if _, ok := next.policies[p.ID]; ok {
			return fmt.Errorf("策略ID重复")
		}
		if err := ValidatePolicy(&p); err != nil {
			return err
		}
		if p.Enabled && p.WAF != nil && p.WAF.Enabled {
			if _, err := prepareWAF(); err != nil {
				return fmt.Errorf("WAF引擎初始化失败: %w", err)
			}
		}
		cp := compiledPolicy{p, CompileIP(p.IPListMode, p.IPList)}
		next.policies[p.ID] = cp
		if p.Enabled && p.Global {
			next.globals = append(next.globals, cp)
		}
	}
	e.snapshot.Store(next)
	return nil
}
func (e *Engine) deny(ip, ua, source, entity, id, reason string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rejected++
	e.nextID++
	event := Event{e.nextID, time.Now().UTC(), source, entity, ip, id, reason, "deny"}
	e.events.append(event, MaxEvents)
	return false
}
func (e *Engine) allowPolicy(p compiledPolicy, ip, ua, source, entity string) bool {
	if !p.conf.Enabled {
		return true
	}
	if !p.ip.Allow(ip) {
		return e.deny(ip, ua, source, entity, p.conf.ID, "IP策略拒绝")
	}
	if source == "web" && !AllowUA(p.conf.UAListMode, p.conf.UAList, ua) {
		return e.deny(ip, ua, source, entity, p.conf.ID, "UA策略拒绝")
	}
	return true
}
func (e *Engine) CheckGlobal(ip, ua, source, entity string) bool {
	if e == nil {
		return true
	}
	// 不对 ip 做 net.ParseIP 归一化：调用方传入的地址来自 net 库（RemoteAddr /
	// UDPAddr）已是规范形式，AddBan 写入的键同样经过规范化，两侧必然一致。
	// 这样每个 TCP 连接 / 每个 UDP 包可省掉一次解析与分配。
	e.mu.Lock()
	ban, ok := e.bans[ip]
	if ok && !ban.ExpiresAt.After(time.Now()) {
		delete(e.bans, ip)
		ok = false
	}
	e.mu.Unlock()
	if ok {
		return e.deny(ip, ua, source, entity, "", "临时封禁")
	}
	for _, p := range e.snapshot.Load().globals {
		if !e.allowPolicy(p, ip, ua, source, entity) {
			return false
		}
	}
	return true
}
func (e *Engine) CheckPolicy(id, ip, ua, source, entity string) bool {
	if e == nil || id == "" {
		return true
	}
	p, ok := e.snapshot.Load().policies[id]
	if !ok {
		return e.deny(ip, ua, source, entity, id, "引用策略不可用")
	}
	return e.allowPolicy(p, ip, ua, source, entity)
}

// RecordRejection records a denial already performed by a legacy rule. Shared
// policy checks record their own decisions and must not call this a second time.
func (e *Engine) RecordRejection(ip, source, entity, reason string) {
	if e != nil {
		e.deny(ip, "", source, entity, "", reason)
	}
}
func (e *Engine) AddBan(b Ban) error {
	parsed := net.ParseIP(strings.TrimSpace(b.IP))
	if parsed == nil {
		return fmt.Errorf("封禁地址必须是IPv4或IPv6")
	}
	b.IP = parsed.String()
	b.Reason = strings.TrimSpace(b.Reason)
	now := time.Now()
	if len(b.Reason) > 512 || !b.ExpiresAt.After(now) || b.ExpiresAt.After(now.Add(30*24*time.Hour)) {
		return fmt.Errorf("封禁有效期须在未来30天内，原因最多512字节")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneBansLocked(now)
	if _, ok := e.bans[b.IP]; !ok && len(e.bans) >= MaxBans {
		return fmt.Errorf("临时封禁已达容量上限")
	}
	e.bans[b.IP] = b
	return nil
}
func (e *Engine) DeleteBan(ip string) {
	if p := net.ParseIP(ip); p != nil {
		ip = p.String()
	}
	e.mu.Lock()
	delete(e.bans, ip)
	e.mu.Unlock()
}
func (e *Engine) pruneBansLocked(now time.Time) {
	for ip, b := range e.bans {
		if !b.ExpiresAt.After(now) {
			delete(e.bans, ip)
		}
	}
}
func (e *Engine) Bans() []Ban {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneBansLocked(time.Now())
	out := make([]Ban, 0, len(e.bans))
	for _, b := range e.bans {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}
func (e *Engine) Events() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.events.newestFirst()
}
func (e *Engine) Summary() Summary {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneBansLocked(time.Now())
	snap := e.snapshot.Load()
	return Summary{len(snap.policies), len(snap.globals), len(e.bans), e.rejected, e.events.len()}
}
