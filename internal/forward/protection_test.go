package forward

import (
	"andey-proxy/internal/config"
	"andey-proxy/internal/guard"
	"context"
	"net"
	"testing"
	"time"
)

// The same client socket retains its UDP session across policy hot reloads.
func TestExistingUDPSessionAppliesHotPolicyAndBan(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	backendDone := make(chan struct{})
	go func() {
		defer close(backendDone)
		b := make([]byte, 128)
		for {
			n, addr, err := backend.ReadFromUDP(b)
			if err != nil {
				return
			}
			backend.WriteToUDP(b[:n], addr)
		}
	}()
	defer func() { backend.Close(); <-backendDone }()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := config.ProtectionPolicy{ID: "p", Name: "shared", Enabled: true, IPListMode: "blacklist"}
	save := func() {
		t.Helper()
		if err := cfg.Update(func(c *config.Config) error { c.ProtectionPolicies = []config.ProtectionPolicy{policy}; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	save()
	engine := guard.NewEngine(cfg)
	svc := NewService(cfg)
	svc.Protection = engine
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.serveUDP(ctx, config.ForwardRule{ID: "udp", PolicyID: "p", Targets: []string{backend.LocalAddr().String()}}, ln)
		close(done)
	}()
	defer func() { cancel(); <-done; svc.wg.Wait() }()
	conn, err := net.DialUDP("udp", nil, ln.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exchange := func(allowed bool) {
		t.Helper()
		conn.SetDeadline(time.Now().Add(400 * time.Millisecond))
		if _, err := conn.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 16)
		n, err := conn.Read(b)
		if allowed && (err != nil || string(b[:n]) != "ping") {
			t.Fatalf("expected echo: %s %v", b[:n], err)
		}
		if !allowed && err == nil {
			t.Fatal("existing UDP session bypassed new restriction")
		}
	}
	exchange(true)
	policy.IPList = []string{"127.0.0.1"}
	save()
	if err := engine.Reload(); err != nil {
		t.Fatal(err)
	}
	exchange(false)
	policy.IPList = nil
	save()
	engine.Reload()
	exchange(true)
	if err := engine.AddBan(guard.Ban{IP: "127.0.0.1", Reason: "fixture", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	exchange(false)
	engine.DeleteBan("127.0.0.1")
	exchange(true)
	if engine.Summary().Rejected != 2 {
		t.Fatalf("rejected=%d", engine.Summary().Rejected)
	}
}
