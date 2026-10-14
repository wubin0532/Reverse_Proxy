package ddns

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func contractFixture(t *testing.T) (*handler, http.Handler, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "unexpected DNS request", 500)
	}))
	t.Cleanup(fake.Close)
	h := ddnsTestHandler(t, config.DNSProviderConf{ID: "provider", Type: "cloudflare", Key: "fixture", Endpoint: fake.URL})
	t.Cleanup(func() { h.w.Stop(); _ = h.cfg.State().Close() })
	router := chi.NewRouter()
	RegisterRoutes(router, h.cfg, h.w)
	return h, router, hits
}
func contractRequest(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data string
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		data = string(encoded)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(data))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
func currentContractTask(t *testing.T, h *handler) config.DDNSTask {
	t.Helper()
	h.cfg.RLock()
	defer h.cfg.RUnlock()
	if len(h.cfg.DDNS) != 1 {
		t.Fatalf("task count=%d", len(h.cfg.DDNS))
	}
	return h.cfg.DDNS[0]
}

func TestCreateAndRecordMutationsFollowUIContract(t *testing.T) {
	h, router, hits := contractFixture(t)
	// Exact new-dialog contract: revision belongs only to edits. Keep task and
	// records disabled, so these real router requests never perform DNS writes.
	body := map[string]any{"name": "dual fixture", "enabled": false, "providerId": "provider", "interval": 300, "forceInterval": 21600, "records": []config.DDNSRecord{{Domain: "home.example.com", Type: "A", TTL: 300, Enabled: false}}, "ipv4Source": config.IPSourceConfig{Source: "interface", Interface: "auto"}, "ipv6Source": config.IPSourceConfig{Source: "interface", Interface: "auto"}}
	rec := contractRequest(t, router, "POST", "/api/ddns/tasks", body)
	if rec.Code != 200 {
		t.Fatalf("create=%d %s", rec.Code, rec.Body.String())
	}
	task := currentContractTask(t, h)
	if task.Records[0].ID == "" {
		t.Fatal("new record lacks stable ID")
	}
	path := "/api/ddns/tasks/" + task.ID
	record := config.DDNSRecord{Name: "v6", Domain: "home.example.com", Type: "AAAA", TTL: 600, Enabled: false}
	rec = contractRequest(t, router, "POST", path+"/records", map[string]any{"revision": taskRevision(task), "record": record})
	if rec.Code != 200 {
		t.Fatalf("record create=%d %s", rec.Code, rec.Body.String())
	}
	stale := taskRevision(task)
	task = currentContractTask(t, h)
	id := task.Records[1].ID
	rec = contractRequest(t, router, "POST", path+"/records/"+id+"/toggle", map[string]any{"revision": stale})
	if rec.Code != 409 {
		t.Fatalf("stale record toggle=%d", rec.Code)
	}
	record = task.Records[1]
	record.Name = "renamed v6"
	rec = contractRequest(t, router, "PUT", path+"/records/"+id, map[string]any{"revision": taskRevision(task), "record": record})
	if rec.Code != 200 {
		t.Fatalf("record edit=%d %s", rec.Code, rec.Body.String())
	}
	task = currentContractTask(t, h)
	rec = contractRequest(t, router, "POST", path+"/records/"+id+"/toggle", map[string]any{"revision": taskRevision(task)})
	if rec.Code != 200 {
		t.Fatalf("record toggle=%d", rec.Code)
	}
	task = currentContractTask(t, h)
	if !task.Records[1].Enabled {
		t.Fatal("toggle not persisted")
	}
	rec = contractRequest(t, router, "DELETE", path+"/records/"+id, map[string]any{"revision": taskRevision(task)})
	if rec.Code != 200 {
		t.Fatalf("record delete=%d", rec.Code)
	}
	task = currentContractTask(t, h)
	rec = contractRequest(t, router, "DELETE", path+"/records/"+task.Records[0].ID, map[string]any{"revision": taskRevision(task)})
	if rec.Code != 400 || len(currentContractTask(t, h).Records) != 1 {
		t.Fatal("last record must remain intact")
	}
	if hits.Load() != 0 {
		t.Fatalf("unexpected remote DNS calls=%d", hits.Load())
	}
}

func TestTaskMutationLegacyOnlyCompatibility(t *testing.T) {
	h, router, hits := contractFixture(t)
	legacy := config.DDNSTask{ID: "legacy", Name: "old task", ProviderID: "provider", Domains: []string{"home.example.com"}, IPType: "ipv4", IPSource: "interface", Interface: "fixture-nonexistent", Interval: 300}
	if err := h.cfg.Update(func(c *config.Config) error { c.DDNS = []config.DDNSTask{legacy}; return nil }); err != nil {
		t.Fatal(err)
	}
	legacy.Name = "old client edited"
	rec := contractRequest(t, router, "PUT", "/api/ddns/tasks/legacy", legacy)
	if rec.Code != 200 {
		t.Fatalf("legacy edit=%d %s", rec.Code, rec.Body.String())
	}
	if len(currentContractTask(t, h).Records) != 0 {
		t.Fatal("legacy client must not be forced to migrate")
	}
	rec = contractRequest(t, router, "POST", "/api/ddns/tasks/legacy/toggle", nil)
	if rec.Code != 200 {
		t.Fatalf("legacy empty toggle=%d", rec.Code)
	}
	rec = contractRequest(t, router, "DELETE", "/api/ddns/tasks/legacy", nil)
	if rec.Code != 200 {
		t.Fatalf("legacy empty delete=%d", rec.Code)
	}
	if hits.Load() != 0 {
		t.Fatal("legacy fixture called remote provider")
	}
}

func TestNewRecordTaskRequiresRevisionWhenLegacyFieldsAreOmitted(t *testing.T) {
	h, router, _ := contractFixture(t)
	task := config.DDNSTask{ID: "new", Name: "new", ProviderID: "provider", Interval: 300, Records: []config.DDNSRecord{{ID: "a", Domain: "home.example.com", Type: "A", Enabled: false}}}
	if err := h.cfg.Update(func(c *config.Config) error { c.DDNS = []config.DDNSTask{task}; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, action := range []struct{ method, path string }{{"POST", "/api/ddns/tasks/new/toggle"}, {"DELETE", "/api/ddns/tasks/new"}} {
		rec := contractRequest(t, router, action.method, action.path, nil)
		if rec.Code != 409 {
			t.Fatalf("versionless %s=%d", action.path, rec.Code)
		}
	}
	legacyBody := config.DDNSTask{ID: "new", Name: "overwrite", ProviderID: "provider", Interval: 300, Domains: []string{"other.example.com"}, IPType: "ipv4", IPSource: "interface"}
	rec := contractRequest(t, router, "PUT", "/api/ddns/tasks/new", legacyBody)
	if rec.Code != 409 {
		t.Fatalf("omitted records versionless PUT=%d", rec.Code)
	}
	if taskRevision(currentContractTask(t, h)) != taskRevision(task) {
		t.Fatal("rejected operation changed configuration")
	}
}
