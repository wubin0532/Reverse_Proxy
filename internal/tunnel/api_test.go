package tunnel

import (
	"encoding/json"
	"strings"
	"testing"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func TestAPISecretsAreWriteOnlyAndBlankUpdatesRetainThem(t *testing.T) {
	m, _, router := newTestManager(t)
	for _, path := range []string{"/api/tunnels/accounts", "/api/tunnels/instances"} {
		w := request(t, router, "GET", path, "")
		if w.Code != 200 || (strings.Contains(w.Body.String(), "SUPER-SECRET") || strings.Contains(w.Body.String(), testTunnelToken)) {
			t.Fatalf("unsafe response: %s", w.Body.String())
		}
	}
	w := request(t, router, "PUT", "/api/tunnels/accounts/account", `{"name":"renamed","accountId":"`+testAccountID+`","token":""}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	a, _ := m.account("account")
	if a.Token != "API-SUPER-SECRET" {
		t.Fatal("blank update cleared token")
	}
	w = request(t, router, "PUT", "/api/tunnels/instances/instance", `{"name":"renamed","source":"managed","accountRef":"account","tunnelId":"`+testTunnelID+`","token":"","protocol":"http2","ipVersion":"6"}`)
	if w.Code != 200 || (strings.Contains(w.Body.String(), "SUPER-SECRET") || strings.Contains(w.Body.String(), testTunnelToken)) {
		t.Fatal(w.Body.String())
	}
	inst, _ := m.instance("instance")
	if inst.Token != testTunnelToken {
		t.Fatal("blank instance token cleared")
	}
}
func TestAPIRejectsMetadataInjectionAndManagementOrigin(t *testing.T) {
	_, _, router := newTestManager(t)
	for _, body := range []string{
		`{"routes":[{"hostname":"app.example.com","zoneId":"` + testZoneID + `","target":"url","url":"http://127.0.0.1:16606"}]}`,
		`{"routes":[{"hostname":"app.example.com","zoneId":"` + testZoneID + `","target":"url","url":"https://admin:secret@example.com"}]}`,
		`{"routes":[{"hostname":"app.example.com","zoneId":"` + testZoneID + `","target":"url","url":"http://192.0.2.1","dnsOwned":true}]}`,
	} {
		w := request(t, router, "PUT", "/api/tunnels/instances/instance/routes", body)
		if w.Code != 400 {
			t.Fatalf("unsafe route accepted: %d %s", w.Code, w.Body.String())
		}
	}
}
func TestTokenOnlyDetachDoesNotContactCloud(t *testing.T) {
	m, f, router := newTestManager(t)
	inst, _ := m.instance("instance")
	inst.Source = "imported"
	inst.AccountRef = ""
	m.saveInstance(inst)
	w := request(t, router, "GET", "/api/tunnels/instances/instance/routes", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"readOnly":true`) {
		t.Fatal(w.Body.String())
	}
	w = request(t, router, "DELETE", "/api/tunnels/instances/instance", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reads != 0 || f.writes != 0 || f.dnsDeletes != 0 {
		t.Fatal("detach mutated cloud")
	}
}
func TestOperationAndRestoreMutationsAreSerialized(t *testing.T) {
	m, _, router := newTestManager(t)
	m.mutation.Lock()
	w := request(t, router, "POST", "/api/tunnels/instances/instance/sync", `{"digest":"x"}`)
	if w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	if _, err := m.LockForRestore(); err == nil {
		t.Fatal("restore allowed during mutation")
	}
	m.mutation.Unlock()
	release, err := m.LockForRestore()
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestTunnelAPIsRequireAuthentication(t *testing.T) {
	m, _, _ := newTestManager(t)
	server := api.NewServer(m.cfg)
	server.Mount(func(r chi.Router) { RegisterRoutes(r, m) })
	w := request(t, server.Router(), "GET", "/api/tunnels/accounts", "")
	if w.Code != 401 {
		t.Fatalf("status %d", w.Code)
	}
}
func TestRouteAdoptionAndDeletionPreserveCleanupMetadata(t *testing.T) {
	m, f, router := newTestManager(t)
	body := `{"digest":"` + digestFor(f) + `","routes":[{"hostname":"keep.example.com","zoneId":"` + testZoneID + `","target":"url","url":"https://192.0.2.20","adoptHostname":"keep.example.com"}]}`
	w := request(t, router, "PUT", "/api/tunnels/instances/instance/routes", body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	inst, _ := m.instance("instance")
	if inst.Routes[0].AppliedService != "http://192.0.2.1:80" {
		t.Fatal("adopted route lost identity")
	}
	inst.Routes[0].DNSRecordID = testRecordID
	inst.Routes[0].DNSOwned = true
	m.saveInstance(inst)
	w = request(t, router, "PUT", "/api/tunnels/instances/instance/routes", `{"routes":[]}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	inst, _ = m.instance("instance")
	if len(inst.RetiredRoutes) != 1 || !inst.RetiredRoutes[0].DNSOwned {
		t.Fatal("cleanup metadata lost")
	}
	var response api.Response
	if json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal("bad JSON")
	}
}
func TestDDNSConflictIsRejected(t *testing.T) {
	m, _, router := newTestManager(t)
	m.cfg.Update(func(c *config.Config) error {
		c.DDNS = []config.DDNSTask{{Enabled: true, Domains: []string{"app.example.com"}}}
		return nil
	})
	w := request(t, router, "PUT", "/api/tunnels/instances/instance/routes", `{"routes":[{"hostname":"app.example.com","zoneId":"`+testZoneID+`","target":"url","url":"http://192.0.2.10"}]}`)
	if w.Code != 400 {
		t.Fatal(w.Body.String())
	}
}
