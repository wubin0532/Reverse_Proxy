// Package firewall 在 OpenWrt 上通过 UCI 自动维护 WAN 侧防火墙放行规则。
// 各模块以来源（source）为单位上报期望放行的完整集合，Manager 合并后
// 与已写入 UCI 的规则做增量增删（reconcile）。非 OpenWrt 环境下所有操作为空操作。
package firewall

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// 规则来源标识，Manager 合并各来源后再统一 reconcile。
const (
	SourceWeb     = "web"     // webproxy 站点
	SourceForward = "forward" // 端口转发规则
)

// 自动添加的防火墙规则 name 前缀，用于识别与清理。
const ruleNamePrefix = "andey-proxy-"

// Rule 一条期望放行的防火墙规则。
type Rule struct {
	Key   string `json:"key"`   // 站点/转发规则 ID
	Port  int    `json:"port"`  // 监听端口
	Proto string `json:"proto"` // tcp / udp / tcpudp
}

// parsedRule 已写入 UCI 的规则及其 section 名。
type parsedRule struct {
	section string
	rule    Rule
}

// Manager 维护 andey-proxy 自动放行的防火墙规则集合，并发安全。
type Manager struct {
	mu      sync.Mutex
	sources map[string]map[string]Rule // 来源 -> Key -> 规则
	added   map[string]parsedRule      // 已写入 UCI 的规则 Key -> 规则
	scanned bool                       // 是否已从 uci show 重建 added

	detectOnce sync.Once
	detected   bool

	// 以下依赖可在测试中替换
	detectFn func() bool                                       // OpenWrt 环境检测
	execFn   func(name string, args ...string) (string, error) // 执行外部命令
	reloadFn func()                                            // 重载防火墙，nil 时用默认实现
}

// NewManager 创建防火墙管理器。
func NewManager() *Manager {
	return &Manager{
		sources:  make(map[string]map[string]Rule),
		added:    make(map[string]parsedRule),
		detectFn: detectOpenWrt,
		execFn:   realExec,
	}
}

// IsOpenWrt 检测当前是否为 OpenWrt 环境（uci 命令与 /etc/config/firewall 均存在），结果只检测一次。
func (m *Manager) IsOpenWrt() bool {
	m.detectOnce.Do(func() {
		m.detected = m.detectFn()
	})
	return m.detected
}

// detectOpenWrt 默认的 OpenWrt 环境检测。
func detectOpenWrt() bool {
	if _, err := exec.LookPath("uci"); err != nil {
		return false
	}
	if _, err := os.Stat("/etc/config/firewall"); err != nil {
		return false
	}
	return true
}

// realExec 默认的命令执行实现。
func realExec(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// SetDesiredFrom 上报某个来源当前期望放行的完整规则集合，
// Manager 合并所有来源后与已写入的规则对比，增量 add/del。
// 非 OpenWrt 环境下仅记录期望集合，不执行任何命令。
func (m *Manager) SetDesiredFrom(source string, rules []Rule) {
	byKey := make(map[string]Rule, len(rules))
	for _, r := range rules {
		if r.Key == "" || r.Port <= 0 || r.Port > 65535 {
			continue
		}
		byKey[r.Key] = r
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources[source] = byKey

	if !m.IsOpenWrt() {
		if len(byKey) > 0 {
			log.Printf("[firewall] 非 OpenWrt 环境，跳过来源 %s 的 %d 条放行规则", source, len(byKey))
		}
		return
	}
	if err := m.reconcileLocked(); err != nil {
		log.Printf("[firewall] 同步防火墙规则失败: %v", err)
	}
}

// Rules 返回当前已写入 UCI 的放行规则（按 Key 排序），用于状态查询。
func (m *Manager) Rules() []Rule {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Rule, 0, len(m.added))
	for _, pr := range m.added {
		out = append(out, pr.rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// reconcileLocked 将合并后的期望集合与已写入集合做增量同步。
// 调用方须持有 m.mu。批量变更只 commit + reload 一次。
func (m *Manager) reconcileLocked() error {
	if !m.scanned {
		m.scanned = true
		if err := m.scanLocked(); err != nil {
			// 扫描失败不阻断：按空集合继续，已有规则会按名重复添加的概率极低
			log.Printf("[firewall] 扫描已有规则失败: %v", err)
		}
	}

	desired := make(map[string]Rule)
	for _, src := range m.sources {
		for k, r := range src {
			desired[k] = r
		}
	}

	var toAdd []Rule
	var toDel []string
	for k, r := range desired {
		if old, ok := m.added[k]; !ok {
			toAdd = append(toAdd, r)
		} else if old.rule != r {
			// 端口/协议变化：先删后加
			toDel = append(toDel, k)
			toAdd = append(toAdd, r)
		}
	}
	for k := range m.added {
		if _, ok := desired[k]; !ok {
			toDel = append(toDel, k)
		}
	}
	if len(toAdd) == 0 && len(toDel) == 0 {
		return nil
	}
	// 排序保证命令顺序稳定（便于测试与排查）
	sort.Strings(toDel)
	sort.Slice(toAdd, func(i, j int) bool { return toAdd[i].Key < toAdd[j].Key })

	for _, key := range toDel {
		sec := m.added[key].section
		if _, err := m.execFn("uci", "delete", "firewall."+sec); err != nil {
			log.Printf("[firewall] 删除规则 %s (section %s) 失败: %v", key, sec, err)
			continue
		}
		delete(m.added, key)
		log.Printf("[firewall] 已删除放行规则 %s", key)
	}
	for _, r := range toAdd {
		if err := m.addRuleLocked(r); err != nil {
			log.Printf("[firewall] 添加规则 %s 失败: %v", r.Key, err)
			continue
		}
	}

	if _, err := m.execFn("uci", "commit", "firewall"); err != nil {
		return fmt.Errorf("uci commit firewall: %w", err)
	}
	m.reloadFirewall()
	return nil
}

// addRuleLocked 通过 uci 添加一条 WAN 侧放行规则。调用方须持有 m.mu。
func (m *Manager) addRuleLocked(r Rule) error {
	out, err := m.execFn("uci", "add", "firewall", "rule")
	if err != nil {
		return err
	}
	sec := strings.TrimSpace(out)
	if sec == "" {
		sec = "@rule[-1]" // uci add 未输出 section 名时回退到最后一条
	}
	proto := r.Proto
	if proto == "tcpudp" {
		proto = "tcp udp"
	}
	sets := [][2]string{
		{"name", ruleNamePrefix + r.Key},
		{"src", "wan"},
		{"proto", proto},
		{"dest_port", strconv.Itoa(r.Port)},
		{"target", "ACCEPT"},
	}
	for _, kv := range sets {
		if _, err := m.execFn("uci", "set", "firewall."+sec+"."+kv[0]+"="+kv[1]); err != nil {
			return err
		}
	}
	m.added[r.Key] = parsedRule{section: sec, rule: r}
	log.Printf("[firewall] 已放行 %s 端口 %d/%s", r.Key, r.Port, r.Proto)
	return nil
}

// scanLocked 从 uci show firewall 输出中重建已添加规则集合（应对进程重启）。
func (m *Manager) scanLocked() error {
	out, err := m.execFn("uci", "show", "firewall")
	if err != nil {
		return err
	}
	for k, pr := range parseAndeyRules(out) {
		m.added[k] = pr
	}
	if len(m.added) > 0 {
		log.Printf("[firewall] 从 UCI 恢复 %d 条已有放行规则", len(m.added))
	}
	return nil
}

// reloadFirewall 重载防火墙使配置生效：优先 /etc/init.d/firewall，其次 fw4。
func (m *Manager) reloadFirewall() {
	if m.reloadFn != nil {
		m.reloadFn()
		return
	}
	if _, err := os.Stat("/etc/init.d/firewall"); err == nil {
		if _, err := m.execFn("/etc/init.d/firewall", "reload"); err == nil {
			return
		}
	}
	if _, err := exec.LookPath("fw4"); err == nil {
		if _, err := m.execFn("fw4", "reload"); err != nil {
			log.Printf("[firewall] fw4 reload 失败: %v", err)
		}
		return
	}
	log.Printf("[firewall] 未找到防火墙 reload 命令")
}

// parseAndeyRules 从 `uci show firewall` 输出中找出所有 name 为 andey-proxy-* 的
// rule section，返回 Key -> 规则（含 section 名，用于删除）。
func parseAndeyRules(show string) map[string]parsedRule {
	attrs := make(map[string]map[string]string) // section -> 属性
	for _, line := range strings.Split(show, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "firewall.") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		left := strings.TrimPrefix(strings.TrimSpace(line[:eq]), "firewall.")
		val := strings.Trim(strings.TrimSpace(line[eq+1:]), "'")
		dot := strings.LastIndexByte(left, '.')
		if dot < 0 {
			// firewall.cfgxxxx=rule 形式的 section 声明
			if attrs[left] == nil {
				attrs[left] = make(map[string]string)
			}
			attrs[left]["type"] = val
			continue
		}
		sec, field := left[:dot], left[dot+1:]
		if attrs[sec] == nil {
			attrs[sec] = make(map[string]string)
		}
		attrs[sec][field] = val
	}

	res := make(map[string]parsedRule)
	for sec, a := range attrs {
		if a["type"] != "rule" {
			continue
		}
		name := a["name"]
		if !strings.HasPrefix(name, ruleNamePrefix) {
			continue
		}
		key := strings.TrimPrefix(name, ruleNamePrefix)
		port, _ := strconv.Atoi(a["dest_port"])
		res[key] = parsedRule{
			section: sec,
			rule:    Rule{Key: key, Port: port, Proto: normalizeProto(a["proto"])},
		}
	}
	return res
}

// normalizeProto 将 UCI 中的 proto 值归一化为 tcp / udp / tcpudp。
func normalizeProto(proto string) string {
	fields := strings.Fields(strings.ToLower(proto))
	hasTCP, hasUDP := false, false
	for _, f := range fields {
		switch f {
		case "tcp":
			hasTCP = true
		case "udp":
			hasUDP = true
		}
	}
	switch {
	case hasTCP && hasUDP:
		return "tcpudp"
	case hasUDP:
		return "udp"
	default:
		return "tcp"
	}
}
