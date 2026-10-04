package tunnel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

const testTunnelID = "11111111-2222-3333-4444-555555555555"
const testAccountID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testZoneID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const testRecordID = "cccccccccccccccccccccccccccccccc"

var testTunnelToken = encodedTestToken(testAccountID, testTunnelID, "TUNNEL-SUPER-SECRET")
var testNewTunnelToken = encodedTestToken(testAccountID, testTunnelID, "NEW-TUNNEL-SECRET")

func encodedTestToken(account, tunnel, secret string) string {
	data, _ := json.Marshal(tokenClaims{AccountID: account, TunnelID: tunnel, Secret: []byte(secret)})
	return base64.StdEncoding.EncodeToString(data)
}

type cloudFixture struct {
	mu                                      sync.Mutex
	remote                                  cloudConfiguration
	records                                 []dnsRecord
	connectors                              []cloudConnection
	created                                 bool
	creates, writes, dnsCreates, dnsDeletes int
	loseCreate, loseDNS, drift, limited     bool
	reads                                   int
	afterWrite                              func()
	loseDelete                              bool
}

func newTestManager(t *testing.T) (*Manager, *cloudFixture, http.Handler) {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cfg.State().Close() })
	if err = cfg.Update(func(c *config.Config) error {
		c.TunnelAccounts = []config.TunnelAccount{{ID: "account", Name: "test", AccountID: testAccountID, Token: "API-SUPER-SECRET"}}
		c.Tunnels = []config.TunnelInstance{{ID: "instance", Name: "test", Source: "managed", AccountRef: "account", TunnelID: testTunnelID, Token: testTunnelToken, Protocol: "auto", IPVersion: "auto"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fixture := &cloudFixture{remote: cloudConfiguration{Config: map[string]json.RawMessage{"ingress": json.RawMessage(`[{"hostname":"keep.example.com","service":"http://192.0.2.1:80","custom":{"keep":true}},{"service":"http_status:404"}]`), "warp-routing": json.RawMessage(`{"enabled":true}`)}}}
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	m := NewManager(cfg, nil, 16606)
	m.clientFactory = func(a config.TunnelAccount) *cloudClient {
		c := newCloudClient(a)
		c.endpoint = server.URL
		c.http = server.Client()
		return c
	}
	t.Cleanup(m.Stop)
	r := chi.NewRouter()
	RegisterRoutes(r, m)
	return m, fixture, r
}
func (f *cloudFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	output := func(result any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result}) }
	fail := func(status int) {
		w.WriteHeader(status)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":999,"message":"test failure"}]}`)
	}
	path := r.URL.Path
	switch {
	case path == "/zones":
		output([]zone{{ID: testZoneID, Name: "example.com"}})
	case strings.HasSuffix(path, "/dns_records") && r.Method == "GET":
		result := []dnsRecord{}
		for _, record := range f.records {
			if record.Name == r.URL.Query().Get("name") {
				result = append(result, record)
			}
		}
		output(result)
	case strings.HasSuffix(path, "/dns_records") && r.Method == "POST":
		f.dnsCreates++
		var record dnsRecord
		json.NewDecoder(r.Body).Decode(&record)
		record.ID = testRecordID
		f.records = append(f.records, record)
		if f.loseDNS {
			f.loseDNS = false
			fail(504)
		} else {
			output(record)
		}
	case strings.Contains(path, "/dns_records/") && r.Method == "DELETE":
		f.dnsDeletes++
		f.records = nil
		output(map[string]string{"id": testRecordID})
	case strings.HasSuffix(path, "/configurations") && r.Method == "GET":
		f.reads++
		if f.drift && f.reads >= 2 {
			f.remote.Config["external"] = json.RawMessage(`true`)
		}
		output(f.remote)
	case strings.HasSuffix(path, "/configurations") && r.Method == "PUT":
		f.writes++
		json.NewDecoder(r.Body).Decode(&f.remote)
		if f.afterWrite != nil {
			f.afterWrite()
		}
		output(f.remote)
	case strings.HasSuffix(path, "/connections"):
		output(f.connectors)
	case strings.HasSuffix(path, "/token"):
		output(testNewTunnelToken)
	case strings.HasSuffix(path, "/cfd_tunnel") && r.Method == "GET":
		result := []cloudTunnel{}
		if f.created {
			result = append(result, cloudTunnel{ID: testTunnelID, ConfigSource: "cloudflare"})
		}
		output(result)
	case strings.HasSuffix(path, "/cfd_tunnel") && r.Method == "POST":
		f.creates++
		if f.limited {
			fail(429)
			return
		}
		f.created = true
		if f.loseCreate {
			f.loseCreate = false
			fail(504)
		} else {
			output(cloudTunnel{ID: testTunnelID, ConfigSource: "cloudflare"})
		}
	case r.Method == "DELETE":
		f.created = false
		if f.loseDelete {
			fail(504)
		} else {
			output(map[string]string{"id": testTunnelID})
		}
	default:
		output(cloudTunnel{ID: testTunnelID, ConfigSource: "cloudflare"})
	}
}
func request(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func setRoutes(t *testing.T, m *Manager) {
	t.Helper()
	inst, _ := m.instance("instance")
	inst.Routes = []config.TunnelRoute{{ID: "route", Hostname: "app.example.com", ZoneID: testZoneID, Target: "url", URL: "http://192.0.2.20:80"}}
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
}
func digestFor(f *cloudFixture) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return configDigest(f.remote)
}
func waitOperation(t *testing.T, m *Manager, op config.TunnelOperation) config.TunnelOperation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := m.cfg.State().TunnelOperation(op.ID)
		if current.Status != "running" {
			if m.mutation.TryLock() {
				m.mutation.Unlock()
				return current
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return op
}
func TestSyncPreservesCloudFieldsAndReconcilesLostDNSResponse(t *testing.T) {
	m, f, _ := newTestManager(t)
	setRoutes(t, m)
	f.loseDNS = true
	op, err := m.submit("instance", "sync", digestFor(f))
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, m, op)
	if result.Status != "succeeded" {
		t.Fatalf("%+v", result)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 || f.dnsCreates != 1 {
		t.Fatalf("writes=%d dns=%d", f.writes, f.dnsCreates)
	}
	if string(f.remote.Config["warp-routing"]) != `{"enabled":true}` || !bytes.Contains(f.remote.Config["ingress"], []byte(`"custom"`)) {
		t.Fatal("unselected fields lost")
	}
	inst, _ := m.instance("instance")
	if !inst.Routes[0].DNSOwned || inst.Routes[0].DNSRecordID != testRecordID {
		t.Fatalf("ownership not saved: %+v", inst.Routes[0])
	}
}
func TestSyncConflictsDoNotWriteCloudConfiguration(t *testing.T) {
	for _, scenario := range []string{"dns", "drift", "connector"} {
		t.Run(scenario, func(t *testing.T) {
			m, f, _ := newTestManager(t)
			setRoutes(t, m)
			switch scenario {
			case "dns":
				f.records = []dnsRecord{{ID: testRecordID, Name: "app.example.com", Type: "A", Content: "192.0.2.10"}}
			case "drift":
				f.drift = true
			case "connector":
				inst, _ := m.instance("instance")
				inst.Source = "imported"
				m.saveInstance(inst)
				f.connectors = []cloudConnection{{ID: "other", Conns: []connectorConnection{{}}}}
			}
			op, err := m.submit("instance", "sync", digestFor(f))
			if err != nil {
				t.Fatal(err)
			}
			result := waitOperation(t, m, op)
			if result.Status != "failed" {
				t.Fatalf("expected failure: %+v", result)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 || f.dnsCreates != 0 {
				t.Fatal("conflict performed a write")
			}
		})
	}
}
func TestCreateLostResponseAndRateLimit(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprint(limited), func(t *testing.T) {
			m, f, _ := newTestManager(t)
			inst, _ := m.instance("instance")
			inst.TunnelID = ""
			inst.Token = ""
			m.saveInstance(inst)
			f.loseCreate = true
			f.limited = limited
			op, err := m.submit("instance", "create", "")
			if err != nil {
				t.Fatal(err)
			}
			result := waitOperation(t, m, op)
			f.mu.Lock()
			creates := f.creates
			f.mu.Unlock()
			if creates != 1 {
				t.Fatalf("create retried blindly: %d", creates)
			}
			if limited && result.Status != "failed" {
				t.Fatal("429 should fail")
			}
			if !limited && result.Status != "succeeded" {
				t.Fatalf("lost response not recovered: %+v", result)
			}
			if !limited {
				saved, _ := m.instance("instance")
				if saved.TunnelID != testTunnelID || saved.Token != testNewTunnelToken {
					t.Fatal("credentials not persisted")
				}
			}
		})
	}
}
func TestCleanupOnlyOwnedUnchangedDNS(t *testing.T) {
	m, f, _ := newTestManager(t)
	c, _ := m.cloud(config.TunnelInstance{AccountRef: "account"})
	inst, _ := m.instance("instance")
	route := config.TunnelRoute{Hostname: "app.example.com", AppliedHostname: "app.example.com", ZoneID: testZoneID, DNSRecordID: testRecordID, DNSOwned: true}
	f.records = []dnsRecord{{ID: testRecordID, Name: route.Hostname, Type: "CNAME", Content: "different.example.com"}}
	if err := m.cleanupDNS(context.Background(), c, inst, route); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	deletes := f.dnsDeletes
	f.mu.Unlock()
	if deletes != 0 {
		t.Fatal("changed DNS deleted")
	}
	f.mu.Lock()
	f.records[0].Content = testTunnelID + ".cfargotunnel.com"
	f.records[0].Comment = dnsMarker(inst, route)
	f.mu.Unlock()
	route.DNSOwned = false
	if err := m.cleanupDNS(context.Background(), c, inst, route); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	deletes = f.dnsDeletes
	f.mu.Unlock()
	if deletes != 0 {
		t.Fatal("borrowed DNS deleted")
	}
	route.DNSOwned = true
	if err := m.cleanupDNS(context.Background(), c, inst, route); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dnsDeletes != 1 {
		t.Fatal("owned DNS not deleted")
	}
}
