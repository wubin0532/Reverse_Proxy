package firewall

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// 模拟 `uci show firewall` 输出：含两条 andey-proxy 规则、一条其他规则、一个 zone。
const fixtureShow = `
firewall.@defaults[0]=defaults
firewall.@defaults[0].input='REJECT'
firewall.@defaults[0].output='ACCEPT'
firewall.lan=zone
firewall.lan.name='lan'
firewall.lan.input='ACCEPT'
firewall.cfg0a1b2c=rule
firewall.cfg0a1b2c.name='andey-proxy-site1'
firewall.cfg0a1b2c.src='wan'
firewall.cfg0a1b2c.proto='tcp'
firewall.cfg0a1b2c.dest_port='8080'
firewall.cfg0a1b2c.target='ACCEPT'
firewall.cfg9z8y7x=rule
firewall.cfg9z8y7x.name='Allow-SSH'
firewall.cfg9z8y7x.src='wan'
firewall.cfg9z8y7x.proto='tcp'
firewall.cfg9z8y7x.dest_port='22'
firewall.cfg9z8y7x.target='ACCEPT'
firewall.cfg5d6e7f=rule
firewall.cfg5d6e7f.name='andey-proxy-fwd1'
firewall.cfg5d6e7f.src='wan'
firewall.cfg5d6e7f.proto='tcp udp'
firewall.cfg5d6e7f.dest_port='13389'
firewall.cfg5d6e7f.target='ACCEPT'
`

func TestParseAndeyRules(t *testing.T) {
	got := parseAndeyRules(fixtureShow)
	if len(got) != 2 {
		t.Fatalf("期望 2 条 andey-proxy 规则，实际 %d: %v", len(got), got)
	}
	r1, ok := got["site1"]
	if !ok {
		t.Fatal("缺少 site1 规则")
	}
	if r1.section != "cfg0a1b2c" || r1.rule.Port != 8080 || r1.rule.Proto != "tcp" {
		t.Errorf("site1 解析错误: %+v", r1)
	}
	r2, ok := got["fwd1"]
	if !ok {
		t.Fatal("缺少 fwd1 规则")
	}
	if r2.section != "cfg5d6e7f" || r2.rule.Port != 13389 || r2.rule.Proto != "tcpudp" {
		t.Errorf("fwd1 解析错误（tcp udp 应归一化为 tcpudp）: %+v", r2)
	}
	// 非 andey-proxy 前缀的规则不应被识别
	if _, ok := got["Allow-SSH"]; ok {
		t.Error("Allow-SSH 不应被识别为自动放行规则")
	}
}

func TestNormalizeProto(t *testing.T) {
	cases := map[string]string{
		"tcp":     "tcp",
		"UDP":     "udp",
		"tcp udp": "tcpudp",
		"udp tcp": "tcpudp",
		"tcpudp":  "tcp", // 异常值回退 tcp
		"":        "tcp",
		"all":     "tcp",
	}
	for in, want := range cases {
		if got := normalizeProto(in); got != want {
			t.Errorf("normalizeProto(%q)=%q, want %q", in, got, want)
		}
	}
}

// fakeUCISection 模拟一条 uci section。
type fakeUCISection struct {
	name  string
	typ   string
	attrs map[string]string
}

// fakeUCI 最小 uci 状态机模拟：支持 add/set/delete/show，供测试追踪真实状态变化。
type fakeUCI struct {
	sections []*fakeUCISection
	added    int
}

// parseFakeShow 把 `uci show firewall` 风格的文本还原为 section 列表。
func parseFakeShow(show string) *fakeUCI {
	f := &fakeUCI{}
	byName := map[string]*fakeUCISection{}
	for _, line := range strings.Split(show, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "firewall.") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		left := strings.TrimPrefix(line[:eq], "firewall.")
		val := strings.Trim(strings.TrimSpace(line[eq+1:]), "'")
		dot := strings.LastIndexByte(left, '.')
		var secName, field string
		if dot < 0 {
			secName = left
		} else {
			secName, field = left[:dot], left[dot+1:]
		}
		sec := byName[secName]
		if sec == nil {
			sec = &fakeUCISection{name: secName, attrs: map[string]string{}}
			byName[secName] = sec
			f.sections = append(f.sections, sec)
		}
		if field == "" {
			sec.typ = val
		} else {
			sec.attrs[field] = val
		}
	}
	return f
}

// show 渲染当前状态为 `uci show firewall` 风格输出。
func (f *fakeUCI) show() string {
	var b strings.Builder
	for _, sec := range f.sections {
		fmt.Fprintf(&b, "firewall.%s=%s\n", sec.name, sec.typ)
		keys := make([]string, 0, len(sec.attrs))
		for k := range sec.attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "firewall.%s.%s='%s'\n", sec.name, k, sec.attrs[k])
		}
	}
	return b.String()
}

// exec 模拟一次 uci 调用。
func (f *fakeUCI) exec(args ...string) (string, error) {
	switch args[0] {
	case "show":
		return f.show(), nil
	case "add":
		// 模拟真实 uci：返回匿名段名
		f.added++
		name := "cfgNEW"
		if f.added > 1 {
			name = fmt.Sprintf("cfgNEW%d", f.added)
		}
		f.sections = append(f.sections, &fakeUCISection{name: name, typ: args[2], attrs: map[string]string{}})
		return name + "\n", nil
	case "set":
		// firewall.<sec>.<k>=<v>
		kv := strings.TrimPrefix(args[1], "firewall.")
		eq := strings.IndexByte(kv, '=')
		dot := strings.LastIndexByte(kv[:eq], '.')
		sec := f.find(kv[:dot])
		if sec == nil {
			return "", fmt.Errorf("Entry not found")
		}
		sec.attrs[kv[dot+1:eq]] = kv[eq+1:]
		return "", nil
	case "delete":
		name := strings.TrimPrefix(args[1], "firewall.")
		for i, sec := range f.sections {
			if sec.name == name {
				f.sections = append(f.sections[:i], f.sections[i+1:]...)
				return "", nil
			}
		}
		return "", fmt.Errorf("Entry not found")
	case "commit":
		return "", nil
	}
	return "", nil
}

func (f *fakeUCI) find(name string) *fakeUCISection {
	for _, sec := range f.sections {
		if sec.name == name {
			return sec
		}
	}
	return nil
}

// newFakeManager 构造使用假 uci 的 Manager，返回记录到的命令列表。
// showOutput 为初始的 `uci show firewall` 状态。
func newFakeManager(openwrt bool, showOutput string) (*Manager, *[]string) {
	cmds := &[]string{}
	fake := parseFakeShow(showOutput)
	m := NewManager()
	m.detectFn = func() bool { return openwrt }
	m.execFn = func(name string, args ...string) (string, error) {
		*cmds = append(*cmds, name+" "+strings.Join(args, " "))
		if name == "uci" && len(args) >= 1 {
			return fake.exec(args...)
		}
		return "", nil
	}
	reloads := 0
	m.reloadFn = func() {
		reloads++
		*cmds = append(*cmds, "RELOAD")
	}
	return m, cmds
}

func hasCmd(cmds []string, substr string) bool {
	for _, c := range cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func countCmd(cmds []string, substr string) int {
	n := 0
	for _, c := range cmds {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

func TestSetDesiredAddsRule(t *testing.T) {
	m, cmds := newFakeManager(true, "")
	m.SetDesiredFrom(SourceWeb, []Rule{{Key: "site1", Port: 8080, Proto: "tcp"}})

	wantParts := []string{
		"uci add firewall rule",
		"firewall.cfgNEW.name=andey-proxy-site1",
		"firewall.cfgNEW.src=wan",
		"firewall.cfgNEW.proto=tcp",
		"firewall.cfgNEW.dest_port=8080",
		"firewall.cfgNEW.target=ACCEPT",
		"uci commit firewall",
		"RELOAD",
	}
	for _, p := range wantParts {
		if !hasCmd(*cmds, p) {
			t.Errorf("缺少命令片段 %q，实际命令: %v", p, *cmds)
		}
	}
	if n := countCmd(*cmds, "uci commit firewall"); n != 1 {
		t.Errorf("批量变更应只 commit 一次，实际 %d 次", n)
	}
	if n := countCmd(*cmds, "RELOAD"); n != 1 {
		t.Errorf("批量变更应只 reload 一次，实际 %d 次", n)
	}

	rules := m.Rules()
	if len(rules) != 1 || rules[0].Key != "site1" || rules[0].Port != 8080 {
		t.Errorf("Rules() 不符: %v", rules)
	}
}

func TestSetDesiredTcpudp(t *testing.T) {
	m, cmds := newFakeManager(true, "")
	m.SetDesiredFrom(SourceForward, []Rule{{Key: "fwd1", Port: 13389, Proto: "tcpudp"}})
	if !hasCmd(*cmds, "firewall.cfgNEW.proto=tcp udp") {
		t.Errorf("tcpudp 应转换为 'tcp udp'，实际命令: %v", *cmds)
	}
}

func TestSetDesiredIdempotent(t *testing.T) {
	m, cmds := newFakeManager(true, "")
	rules := []Rule{{Key: "site1", Port: 8080, Proto: "tcp"}}
	m.SetDesiredFrom(SourceWeb, rules)
	before := len(*cmds)
	m.SetDesiredFrom(SourceWeb, rules) // 同样集合再来一次
	if len(*cmds) != before {
		t.Errorf("相同期望集合不应产生新命令，新增: %v", (*cmds)[before:])
	}
}

func TestSetDesiredDeletesRemoved(t *testing.T) {
	m, cmds := newFakeManager(true, "")
	m.SetDesiredFrom(SourceWeb, []Rule{{Key: "site1", Port: 8080, Proto: "tcp"}})
	*cmds = nil
	m.SetDesiredFrom(SourceWeb, nil) // 站点删除/关闭自动放行
	if !hasCmd(*cmds, "uci delete firewall.cfgNEW") {
		t.Errorf("缺少删除命令，实际: %v", *cmds)
	}
	if !hasCmd(*cmds, "uci commit firewall") || !hasCmd(*cmds, "RELOAD") {
		t.Errorf("删除后应 commit + reload，实际: %v", *cmds)
	}
	if len(m.Rules()) != 0 {
		t.Errorf("删除后 Rules() 应为空，实际: %v", m.Rules())
	}
}

func TestMergeSources(t *testing.T) {
	m, cmds := newFakeManager(true, "")
	m.SetDesiredFrom(SourceWeb, []Rule{{Key: "site1", Port: 8080, Proto: "tcp"}})
	m.SetDesiredFrom(SourceForward, []Rule{{Key: "fwd1", Port: 13389, Proto: "udp"}})
	if len(m.Rules()) != 2 {
		t.Fatalf("合并后应有 2 条规则，实际: %v", m.Rules())
	}

	// web 来源清空不应影响 forward 来源
	*cmds = nil
	m.SetDesiredFrom(SourceWeb, nil)
	if !hasCmd(*cmds, "uci delete") {
		t.Errorf("应删除 site1，实际: %v", *cmds)
	}
	rules := m.Rules()
	if len(rules) != 1 || rules[0].Key != "fwd1" {
		t.Errorf("forward 来源规则不应受影响，实际: %v", rules)
	}
}

func TestStartupScanRestoresAndDeletesStale(t *testing.T) {
	// 模拟进程重启：UCI 中已有 site1（与期望一致）和 fwd1（已过期）
	m, cmds := newFakeManager(true, fixtureShow)
	m.SetDesiredFrom(SourceWeb, []Rule{{Key: "site1", Port: 8080, Proto: "tcp"}})
	m.SetDesiredFrom(SourceForward, nil)

	if !hasCmd(*cmds, "uci delete firewall.cfg5d6e7f") {
		t.Errorf("应删除已过期规则 fwd1 (cfg5d6e7f)，实际: %v", *cmds)
	}
	if hasCmd(*cmds, "name=andey-proxy-site1") {
		t.Errorf("site1 与已有一致，不应重复添加，实际: %v", *cmds)
	}
	if hasCmd(*cmds, "uci delete firewall.cfg0a1b2c") {
		t.Errorf("site1 不应被删除，实际: %v", *cmds)
	}
	rules := m.Rules()
	if len(rules) != 1 || rules[0].Key != "site1" {
		t.Errorf("最终应只剩 site1，实际: %v", rules)
	}
}

func TestProtoChangeReplacesRule(t *testing.T) {
	// 已有 tcp 规则，期望变为 tcpudp：应先删后加
	m, cmds := newFakeManager(true, fixtureShow)
	m.SetDesiredFrom(SourceWeb, nil)
	m.SetDesiredFrom(SourceForward, []Rule{{Key: "fwd1", Port: 13389, Proto: "tcp"}})
	// fwd1 在 fixture 中是 tcpudp，期望 tcp → 触发替换
	if !hasCmd(*cmds, "uci delete firewall.cfg5d6e7f") {
		t.Errorf("协议变化应先删除旧规则，实际: %v", *cmds)
	}
	if !hasCmd(*cmds, "name=andey-proxy-fwd1") || !hasCmd(*cmds, "firewall.cfgNEW.proto=tcp") {
		t.Errorf("协议变化应重新添加，实际: %v", *cmds)
	}
}

func TestNonOpenWrtNoop(t *testing.T) {
	m, cmds := newFakeManager(false, "")
	m.SetDesiredFrom(SourceWeb, []Rule{{Key: "site1", Port: 8080, Proto: "tcp"}})
	if len(*cmds) != 0 {
		t.Errorf("非 OpenWrt 环境不应执行任何命令，实际: %v", *cmds)
	}
	if m.IsOpenWrt() {
		t.Error("IsOpenWrt() 应为 false")
	}
	if len(m.Rules()) != 0 {
		t.Errorf("非 OpenWrt 环境 Rules() 应为空，实际: %v", m.Rules())
	}
}

func TestInvalidRuleIgnored(t *testing.T) {
	m, cmds := newFakeManager(true, "")
	m.SetDesiredFrom(SourceWeb, []Rule{
		{Key: "", Port: 8080, Proto: "tcp"},
		{Key: "bad", Port: 0, Proto: "tcp"},
		{Key: "bad2", Port: 70000, Proto: "tcp"},
	})
	if hasCmd(*cmds, "uci add") {
		t.Errorf("非法规则不应产生 add 命令，实际: %v", *cmds)
	}
}
