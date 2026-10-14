package guard

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func trapFixture(t *testing.T) (*config.Config, *Engine) {
	t.Helper()
	c, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	t.Cleanup(e.Trap.Stop)
	return c, e
}
func freeTrapPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func TestTrapListenerExemptionsFailureRollbackAndStop(t *testing.T) {
	c, e := trapFixture(t)
	p := freeTrapPort(t)
	conf := config.TCPTrap{Enabled: true, ListenHost: "127.0.0.1", Ports: []int{p}, BanSeconds: 1}
	if err := e.Trap.Apply(conf, true); err != nil {
		t.Fatal(err)
	}
	addr := e.Trap.Status().Listening[0]
	conn, err := net.DialTimeout("tcp4", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	deadline := time.Now().Add(time.Second)
	for e.Trap.Status().Exempted == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.Trap.Status().Exempted != 1 || len(e.Bans()) != 0 {
		t.Fatal("loopback was captured or connection not processed")
	}
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	bad := conf
	bad.Ports = []int{busy.Addr().(*net.TCPAddr).Port}
	if err = e.Trap.Apply(bad, true); err == nil {
		t.Fatal("busy port accepted")
	}
	if !e.Trap.Status().Running || e.Trap.Status().Listening[0] != addr {
		t.Fatal("failed edit killed old listener")
	}
	reloaded, err := config.Load(c.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TCPTrap.Ports[0] != p {
		t.Fatal("failed edit changed saved config")
	}
	conf.Enabled = false
	if err = e.Trap.Apply(conf, true); err != nil {
		t.Fatal(err)
	}
	if e.Trap.Status().Running {
		t.Fatal("disabled trap running")
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		t.Fatal("disabled trap retained socket:", err)
	}
	ln.Close()
}

func TestTrapBanBusinessReleaseExpiryAndWhitelist(t *testing.T) {
	_, e := trapFixture(t)
	e.Trap.mu.Lock()
	e.Trap.conf = config.TCPTrap{BanSeconds: 3600}
	e.Trap.exempt = CompileIP("whitelist", []string{"192.0.2.0/24", "2001:db8:1::/48"})
	e.Trap.captureLocked("192.0.2.9", ":18870")
	e.Trap.captureLocked("2001:db8:1::9", ":18870")
	e.Trap.captureLocked("203.0.113.9", ":18870")
	e.Trap.captureLocked("2001:db8:2::9", ":18870")
	e.Trap.mu.Unlock()
	if len(e.Bans()) != 2 || e.Trap.Status().Captured != 2 || e.Trap.Status().Exempted != 2 {
		t.Fatal("capture/exempt mismatch")
	}
	for _, source := range []string{"web", "tcp", "udp"} {
		if e.CheckGlobal("203.0.113.9", "", source, "fixture") {
			t.Fatal("business bypass", source)
		}
	}
	e.DeleteBan("203.0.113.9")
	if !e.CheckGlobal("203.0.113.9", "", "web", "fixture") {
		t.Fatal("manual release ineffective")
	}
	e.mu.Lock()
	b := e.bans["2001:db8:2::9"]
	b.ExpiresAt = time.Now().Add(-time.Second)
	e.bans[b.IP] = b
	e.mu.Unlock()
	if !e.CheckGlobal(b.IP, "", "tcp", "fixture") || len(e.Bans()) != 0 {
		t.Fatal("expired ban still blocks")
	}
	found := false
	for _, ev := range e.Events() {
		if ev.Source == "trap" && ev.Action == "ban" {
			found = true
		}
	}
	if !found {
		t.Fatal("trap capture event missing")
	}
}

func TestTrapAPIPersistenceConflictsAndReload(t *testing.T) {
	c, e := trapFixture(t)
	r := chi.NewRouter()
	RegisterRoutes(r, c, e)
	call := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("PUT", "/api/protection/trap", strings.NewReader(body)))
		return w
	}
	if res := call(`{"enabled":true,"ports":[16606],"banSeconds":1}`); res.Code != 400 {
		t.Fatal(res.Body.String())
	}
	if res := call(`{"enabled":true,"ports":[18870,18870]}`); res.Code != 400 {
		t.Fatal("duplicate accepted")
	}
	if res := call(`{"allowIps":["bad"],"ports":[18870]}`); res.Code != 400 {
		t.Fatal("invalid exemption")
	}
	conf := config.TCPTrap{Enabled: true, ListenHost: "127.0.0.1", Ports: []int{freeTrapPort(t)}, AllowIPs: []string{"192.0.2.0/24"}, BanSeconds: 60}
	body, _ := json.Marshal(conf)
	if res := call(string(body)); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	e.Trap.Stop()
	if err := e.Trap.Reload(); err != nil || !e.Trap.Status().Running {
		t.Fatal("saved listeners not restored", err)
	}
	e.Trap.Stop()
	busy, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconvPort(conf.Ports[0])))
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if e.Trap.Reload() == nil || e.Trap.Status().Running || e.Trap.Status().Error == "" {
		t.Fatal("startup failure falsely running")
	}
}

func strconvPort(port int) string { return fmt.Sprintf("%d", port) }

func TestTrapConcurrentApplyStopAndCapture(t *testing.T) {
	_, e := trapFixture(t)
	conf := config.TCPTrap{ListenHost: "127.0.0.1", BanSeconds: 60}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				_ = e.Trap.Apply(conf, false)
				e.Trap.Stop()
				_ = e.Trap.Status()
			}
		}()
	}
	wg.Wait()
}
