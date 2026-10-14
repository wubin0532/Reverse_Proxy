package guard

import (
	"andey-proxy/internal/config"
	"bytes"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedPolicyHotReloadBanAndBoundedEvents(t *testing.T) {
	c, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	p := config.ProtectionPolicy{ID: "p", Name: "shared", Enabled: true, IPListMode: "blacklist", IPList: []string{"203.0.113.0/24"}}
	if err = c.Update(func(c *config.Config) error { c.ProtectionPolicies = []config.ProtectionPolicy{p}; return nil }); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	if e.CheckPolicy("p", "203.0.113.9", "", "tcp", "f") {
		t.Fatal("bound TCP source bypassed policy")
	}
	if !e.CheckGlobal("203.0.113.9", "", "tcp", "f") {
		t.Fatal("unbound policy became global")
	}
	p.IPList = []string{"198.51.100.0/24"}
	p.Global = true
	if err = c.Update(func(c *config.Config) error { c.ProtectionPolicies = []config.ProtectionPolicy{p}; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = e.Reload(); err != nil {
		t.Fatal(err)
	}
	if !e.CheckPolicy("p", "203.0.113.9", "", "udp", "f") || e.CheckGlobal("198.51.100.9", "", "udp", "f") {
		t.Fatal("hot policy update not applied")
	}
	if err = e.AddBan(Ban{IP: "2001:0db8::1", Reason: "fixture", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if e.CheckGlobal("2001:db8::1", "", "web", "s") {
		t.Fatal("canonical IPv6 bypassed ban")
	}
	e.DeleteBan("2001:db8::1")
	if !e.CheckGlobal("2001:db8::1", "", "web", "s") {
		t.Fatal("ban release failed")
	}
	for i := 0; i < MaxEvents+10; i++ {
		e.CheckGlobal("198.51.100.9", "", "web", "s")
	}
	if len(e.Events()) != MaxEvents {
		t.Fatal("event memory not bounded")
	}
	e.mu.Lock()
	e.bans["192.0.2.9"] = Ban{IP: "192.0.2.9", ExpiresAt: time.Now().Add(-time.Second)}
	e.mu.Unlock()
	if !e.CheckGlobal("192.0.2.9", "", "udp", "f") {
		t.Fatal("expired ban remains effective")
	}
}

func TestProtectionAPIReferenceAndInvalidBan(t *testing.T) {
	c, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	r := chi.NewRouter()
	RegisterRoutes(r, c, e)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return rec
	}
	created := call(http.MethodPost, "/api/protection/policies", `{"name":"fixture","enabled":true,"ipListMode":"blacklist","ipList":["203.0.113.9"]}`)
	if created.Code != 200 {
		t.Fatal(created.Body.String())
	}
	var v struct {
		Data config.ProtectionPolicy `json:"data"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if err = c.Update(func(c *config.Config) error { c.Sites = []config.Site{{ID: "s", PolicyID: v.Data.ID}}; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodDelete, "/api/protection/policies/"+v.Data.ID, ""); got.Code != 409 {
		t.Fatalf("referenced deletion: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodPost, "/api/protection/bans", `{"ip":"not-an-ip","expiresAt":"2099-01-01T00:00:00Z"}`); got.Code != 400 {
		t.Fatal("invalid ban accepted")
	}
}
