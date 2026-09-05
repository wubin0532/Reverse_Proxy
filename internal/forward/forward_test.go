package forward

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/guard"
)

func TestAllowIP(t *testing.T) {
	cases := []struct {
		mode string
		list []string
		ip   string
		want bool
	}{
		{"", nil, "1.2.3.4", true},
		{"whitelist", []string{"1.2.3.0/24"}, "1.2.3.4", true},
		{"whitelist", []string{"1.2.3.0/24"}, "5.6.7.8", false},
		{"blacklist", []string{"1.2.3.4"}, "1.2.3.4", false},
		{"blacklist", []string{"1.2.3.4"}, "5.6.7.8", true},
		{"whitelist", []string{"2001:db8::/32"}, "2001:db8::1", true},
	}
	for _, c := range cases {
		if got := guard.AllowIP(c.mode, c.list, c.ip); got != c.want {
			t.Errorf("AllowIP(%q,%v,%q)=%v, want %v", c.mode, c.list, c.ip, got, c.want)
		}
	}
}

func TestValidateRuleNormalizesAndRejectsInvalidConfig(t *testing.T) {
	rule := config.ForwardRule{Name: " test ", Proto: "", Listen: "12345", Targets: []string{"127.0.0.1:80"}}
	if err := validateRule(&rule); err != nil {
		t.Fatal(err)
	}
	if rule.Name != "test" || rule.Proto != "tcp" || rule.Listen != ":12345" {
		t.Fatalf("规则未规范化: %+v", rule)
	}
	bad := []config.ForwardRule{
		{Name: "x", Proto: "icmp", Listen: ":80", Targets: []string{"127.0.0.1:80"}},
		{Name: "x", Proto: "tcp", Listen: ":0", Targets: []string{"127.0.0.1:80"}},
		{Name: "x", Proto: "tcp", Listen: ":80"},
		{Name: "x", Proto: "tcp", Listen: ":80", Targets: []string{"missing-port"}},
		{Name: "x", Proto: "tcp", Listen: ":80", Targets: []string{"127.0.0.1:80"}, IPListMode: "whitelist", IPList: []string{"not-an-ip"}},
	}
	for i := range bad {
		if err := validateRule(&bad[i]); err == nil {
			t.Fatalf("无效规则 %d 未被拒绝: %+v", i, bad[i])
		}
	}
}

func TestTCPForward(t *testing.T) {
	// 起后端 echo 服务
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 128)
				n, _ := c.Read(buf)
				c.Write(buf[:n])
				c.Close()
			}()
		}
	}()
	backendPort := backend.Addr().(*net.TCPAddr).Port

	// 起转发服务
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddr := ln.Addr().String()
	ln.Close()

	cfg := &config.Config{
		Forwards: []config.ForwardRule{{
			ID:      "t1",
			Name:    "test",
			Enabled: true,
			Proto:   "tcp",
			Listen:  listenAddr,
			Targets: []string{fmt.Sprintf("127.0.0.1:%d", backendPort)},
		}},
	}
	svc := NewService(cfg)
	svc.Start()
	defer svc.Stop()
	time.Sleep(200 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", listenAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("连接转发端口失败: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("读取回显失败: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("回显内容不符: %q", buf)
	}
}

func TestReloadStopsDisabled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := &config.Config{
		Forwards: []config.ForwardRule{{
			ID: "t2", Enabled: true, Proto: "tcp",
			Listen: addr, Targets: []string{"127.0.0.1:1"},
		}},
	}
	svc := NewService(cfg)
	svc.Start()
	defer svc.Stop()
	time.Sleep(100 * time.Millisecond)

	// 禁用后 Reload，端口应释放
	cfg.Forwards[0].Enabled = false
	svc.Reload()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			if strings.Contains(err.Error(), "refused") {
				return // 成功释放
			}
		} else {
			c.Close()
		}
		if time.Now().After(deadline) {
			t.Fatal("禁用后端口未释放")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestTCPHalfClose 客户端写完请求后 CloseWrite 半关闭，
// 后端读到 EOF 再延迟回写，转发层应完整传回响应而不是提前拆连接。
func TestTCPHalfClose(t *testing.T) {
	response := strings.Repeat("pong-", 10000) // 50KB，确认无截断
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				io.ReadAll(c) // 等客户端半关闭
				time.Sleep(200 * time.Millisecond)
				c.Write([]byte(response))
				c.Close()
			}()
		}
	}()
	backendPort := backend.Addr().(*net.TCPAddr).Port

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddr := ln.Addr().String()
	ln.Close()

	cfg := &config.Config{
		Forwards: []config.ForwardRule{{
			ID: "t-half", Enabled: true, Proto: "tcp",
			Listen:  listenAddr,
			Targets: []string{fmt.Sprintf("127.0.0.1:%d", backendPort)},
		}},
	}
	svc := NewService(cfg)
	svc.Start()
	defer svc.Stop()

	conn, err := net.DialTimeout("tcp", listenAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("连接转发端口失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	// 半关闭：不再写，但仍应读得到完整响应
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if string(got) != response {
		t.Fatalf("响应被截断: 收到 %d 字节，期望 %d 字节", len(got), len(response))
	}
}

// TestUDPReplySurvivesTargetSilence 目标端静默导致回包读超时后：
// 1. 客户端持续发包期间回包 goroutine 不应退出，目标恢复后仍能收到回包；
// 2. 客户端也空闲导致 goroutine 退出后，再次发包应重建会话恢复回包。
func TestUDPReplySurvivesTargetSilence(t *testing.T) {
	old := udpSessionTimeout
	udpSessionTimeout = 300 * time.Millisecond
	defer func() { udpSessionTimeout = old }()

	// 后端 echo，mute 期间只收不回模拟目标静默
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	var mute atomic.Bool
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := backend.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if mute.Load() {
				continue
			}
			backend.WriteToUDP(buf[:n], addr)
		}
	}()
	backendPort := backend.LocalAddr().(*net.UDPAddr).Port

	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	listenAddr := ln.LocalAddr().String()
	ln.Close()

	cfg := &config.Config{
		Forwards: []config.ForwardRule{{
			ID: "t-udp", Enabled: true, Proto: "udp",
			Listen:  listenAddr,
			Targets: []string{fmt.Sprintf("127.0.0.1:%d", backendPort)},
		}},
	}
	svc := NewService(cfg)
	svc.Start()
	defer svc.Stop()

	client, err := net.DialUDP("udp", nil, mustResolveUDP(t, listenAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	send := func(msg string) {
		if _, err := client.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(msg string) {
		client.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 1500)
		n, err := client.Read(buf)
		if err != nil {
			t.Fatalf("未收到回包 %q: %v", msg, err)
		}
		if string(buf[:n]) != msg {
			t.Fatalf("回包内容不符: %q, want %q", buf[:n], msg)
		}
	}

	// 正常收发
	send("hello")
	expect("hello")

	// 目标静默约 700ms（超过 300ms 读超时），期间客户端持续发包
	mute.Store(true)
	for i := 0; i < 7; i++ {
		send("keepalive")
		time.Sleep(100 * time.Millisecond)
	}

	// 目标恢复：客户端一直活跃，回包 goroutine 应仍在工作
	mute.Store(false)
	send("back")
	expect("back")

	// 客户端也空闲，回包 goroutine 超时退出
	mute.Store(true)
	send("idle")
	time.Sleep(600 * time.Millisecond)

	// 再次发包：主循环检测到 goroutine 已退出，重建会话
	mute.Store(false)
	send("rebuilt")
	expect("rebuilt")
}

func mustResolveUDP(t *testing.T, addr string) *net.UDPAddr {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestReloadReturnsErrorOnPortConflict 端口被占用时 Reload 应返回错误，
// 供 API 层回滚配置并返回 409。
func TestReloadReturnsErrorOnPortConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	cfg := &config.Config{
		Forwards: []config.ForwardRule{{
			ID: "t-conflict", Enabled: true, Proto: "tcp",
			Listen: addr, Targets: []string{"127.0.0.1:1"},
		}},
	}
	svc := NewService(cfg)
	defer svc.Stop()
	if err := svc.Reload(); err == nil {
		t.Fatal("端口被占用时 Reload 应返回错误")
	}
}
