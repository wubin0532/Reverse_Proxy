package tunnel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func changedCloudRoute(t *testing.T, retired bool) (*Manager, *cloudFixture) {
	t.Helper()
	m, f, _ := newTestManager(t)
	inst, _ := m.instance("instance")
	if retired {
		inst.RetiredRoutes = []config.TunnelRoute{publishedTestRoute()}
	} else {
		inst.Routes = []config.TunnelRoute{publishedTestRoute()}
	}
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	f.remote.Config["ingress"] = json.RawMessage(`[{"hostname":"keep.example.com","service":"http://192.0.2.99:80","custom":{"keep":true}},{"service":"http_status:404"}]`)
	return m, f
}

func testRoutesRouter(m *Manager) *chi.Mux {
	r := chi.NewRouter()
	RegisterRoutes(r, m)
	return r
}

func TestCloudDriftRequiresExplicitAcknowledgmentThenCanSyncOrRemove(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(map[bool]string{false: "update", true: "remove"}[retired], func(t *testing.T) {
			m, f := changedCloudRoute(t, retired)
			h := testRoutesRouter(m)
			resp := request(t, h, "GET", "/api/tunnels/instances/instance/routes", "")
			var response struct {
				Data struct {
					Digest    string          `json:"digest"`
					Conflicts []routeConflict `json:"conflicts"`
				} `json:"data"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Data.Conflicts) != 1 || !response.Data.Conflicts[0].Recoverable || response.Data.Conflicts[0].Removing != retired {
				t.Fatal(resp.Body.String())
			}
			op := config.TunnelOperation{ID: "unconfirmed", InstanceID: "instance"}
			if err := m.syncCloud(context.Background(), &op, response.Data.Digest); err == nil {
				t.Fatal("refresh implicitly accepted cloud changes")
			}
			if f.writes != 0 {
				t.Fatal("unconfirmed sync wrote cloud configuration")
			}
			body := `{"digest":"` + response.Data.Digest + `","hostnames":["keep.example.com"]}`
			resp = request(t, h, "POST", "/api/tunnels/instances/instance/routes/reconcile", body)
			if resp.Code != 200 {
				t.Fatal(resp.Body.String())
			}
			inst, _ := m.instance("instance")
			routes := inst.Routes
			if retired {
				routes = inst.RetiredRoutes
			}
			if routes[0].AppliedService != "http://192.0.2.99:80" || routes[0].URL != "http://192.0.2.1:80" {
				t.Fatal("confirmation changed desired target or failed to rebind")
			}
			if f.writes != 0 || f.dnsCreates != 0 {
				t.Fatal("acknowledgment mutated cloud resources")
			}
			job, err := m.submit("instance", "sync", digestFor(f))
			if err != nil {
				t.Fatal(err)
			}
			if result := waitOperation(t, m, job); result.Status != "succeeded" {
				t.Fatalf("%+v", result)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if retired && strings.Contains(string(f.remote.Config["ingress"]), "keep.example.com") {
				t.Fatal("retired route could not be removed")
			}
			if !retired && (!strings.Contains(string(f.remote.Config["ingress"]), "http://192.0.2.1:80") || !strings.Contains(string(f.remote.Config["ingress"]), `"custom"`)) {
				t.Fatal("desired target or unrelated fields lost")
			}
		})
	}
}

func TestReconcileRejectsStaleUnselectedSharedAndAmbiguousRoutes(t *testing.T) {
	for _, scenario := range []string{"stale", "unselected", "shared", "duplicate", "credential", "save-failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, f := changedCloudRoute(t, false)
			digest, hostname, expected := digestFor(f), "keep.example.com", 400
			switch scenario {
			case "stale":
				f.remote.Config["external"] = json.RawMessage(`true`)
				expected = 409
			case "unselected":
				hostname = "foreign.example.com"
			case "shared":
				f.connectors = []cloudConnection{{ID: "other", Conns: []connectorConnection{{}}}}
				expected = 409
			case "duplicate":
				f.remote.Config["ingress"] = json.RawMessage(`[{"hostname":"keep.example.com","service":"http://192.0.2.99:80"},{"hostname":"keep.example.com","service":"http://192.0.2.98:80"},{"service":"http_status:404"}]`)
				digest = digestFor(f)
			case "credential":
				f.remote.Config["ingress"] = json.RawMessage(`[{"hostname":"keep.example.com","service":"http://user:private@example.com"},{"service":"http_status:404"}]`)
				digest = digestFor(f)
			case "save-failure":
				if err := os.Mkdir(filepath.Join(m.cfg.Dir(), "config.json.tmp"), 0700); err != nil {
					t.Fatal(err)
				}
				expected = 500
			}
			resp := request(t, testRoutesRouter(m), "POST", "/api/tunnels/instances/instance/routes/reconcile", `{"digest":"`+digest+`","hostnames":["`+hostname+`"]}`)
			if resp.Code != expected || strings.Contains(resp.Body.String(), "private") {
				t.Fatal(resp.Body.String())
			}
			inst, _ := m.instance("instance")
			if inst.Routes[0].AppliedService != publishedTestRoute().AppliedService || f.writes != 0 {
				t.Fatal("failed reconciliation changed ownership or cloud state")
			}
		})
	}
}

func TestReconcileOnlyAcknowledgesSelectedHostname(t *testing.T) {
	m, f := changedCloudRoute(t, false)
	inst, _ := m.instance("instance")
	other := publishedTestRoute()
	other.ID, other.Hostname, other.AppliedHostname = "other", "other.example.com", "other.example.com"
	inst.Routes = append(inst.Routes, other)
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	f.remote.Config["ingress"] = json.RawMessage(`[{"hostname":"keep.example.com","service":"http://192.0.2.99:80"},{"hostname":"other.example.com","service":"http://192.0.2.98:80"},{"service":"http_status:404"}]`)
	resp := request(t, testRoutesRouter(m), "POST", "/api/tunnels/instances/instance/routes/reconcile", `{"digest":"`+digestFor(f)+`","hostnames":["keep.example.com"]}`)
	if resp.Code != 200 {
		t.Fatal(resp.Body.String())
	}
	inst, _ = m.instance("instance")
	if inst.Routes[0].AppliedService != "http://192.0.2.99:80" || inst.Routes[1].AppliedService != other.AppliedService {
		t.Fatal("unselected route acknowledged")
	}
}
