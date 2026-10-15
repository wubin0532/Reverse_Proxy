package guard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func Test404OnlyProtectionReclaimsExpiredCapacity(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	if err := cfg.Update(func(c *config.Config) error {
		c.ProtectionPolicies = []config.ProtectionPolicy{{ID: "404", Name: "404 only", Enabled: true, Global: true, CC: &config.CCConfig{Enabled: true, WindowSeconds: 3600, MaxRequests: 0, NotFoundThreshold: 2, BanSeconds: 10}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(cfg)
	for i := 0; i < 4096; i++ {
		e.ObserveHTTPStatus(fmt.Sprintf("198.18.%d.%d", i/256, i%256), "rule", http.StatusNotFound, nil)
	}
	if len(e.cc.counts) != 4096 {
		t.Fatal("fixture did not reach capacity:", len(e.cc.counts))
	}
	// Age actual response counters instead of sleeping through a wall-clock
	// window; subsequent requests still go through the public engine methods.
	e.cc.mu.Lock()
	expired := time.Now().Add(-2 * time.Hour)
	e.cc.lastPrune = expired
	for key, counter := range e.cc.counts {
		counter.at = expired
		e.cc.counts[key] = counter
	}
	e.cc.mu.Unlock()
	target := "203.0.113.9"
	for i := 0; i < 2; i++ {
		if !e.InspectHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://app.test/missing", nil), target, "rule", nil) {
			t.Fatal("unexpected rejection before observing the response")
		}
		e.ObserveHTTPStatus(target, "rule", http.StatusNotFound, nil)
	}
	if bans := e.Bans(); len(bans) != 1 || bans[0].IP != target {
		t.Fatal("expired capacity disabled consecutive-404 protection:", bans)
	}
	if len(e.cc.counts) != 1 {
		t.Fatal("expired clients retained:", len(e.cc.counts))
	}
}

func Test404OnlyProtectionDoesNotAllocateForSuccessfulClients(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	if err := cfg.Update(func(c *config.Config) error {
		c.ProtectionPolicies = []config.ProtectionPolicy{{ID: "404", Name: "404 only", Enabled: true, Global: true, CC: &config.CCConfig{Enabled: true, WindowSeconds: 60, NotFoundThreshold: 2, BanSeconds: 10}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(cfg)
	for i := 0; i < 4097; i++ {
		e.ObserveHTTPStatus(fmt.Sprintf("198.18.%d.%d", i/256, i%256), "rule", http.StatusOK, nil)
	}
	if len(e.cc.counts) != 0 {
		t.Fatal("successful clients exhausted 404 tracking:", len(e.cc.counts))
	}
	ip := "203.0.113.9"
	for _, status := range []int{404, 200, 404} {
		e.ObserveHTTPStatus(ip, "rule", status, nil)
	}
	if len(e.Bans()) != 0 {
		t.Fatal("successful response failed to reset the 404 sequence")
	}
	e.ObserveHTTPStatus(ip, "rule", 404, nil)
	if len(e.Bans()) != 1 {
		t.Fatal("consecutive 404s no longer trigger a ban")
	}
}
