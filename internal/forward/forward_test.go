package forward

import (
	"fmt"
	"net"
	"strings"
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
