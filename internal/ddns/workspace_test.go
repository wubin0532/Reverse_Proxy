package ddns

import (
	"andey-proxy/internal/config"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Simulates actual remote DNS state, including drift and partial failures.
func TestRecordReconcileConfigurationDriftAndPartialFailure(t *testing.T) {
	var mu sync.Mutex
	records := map[string]cfRecord{}
	writes, queries := 0, 0
	failV6 := false
	dns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/zones" {
			fmt.Fprint(w, `{"success":true,"result":[{"id":"z"}]}`)
			return
		}
		if r.Method == "GET" {
			queries++
			name, typ := r.URL.Query().Get("name"), r.URL.Query().Get("type")
			if failV6 && typ == "AAAA" {
				fmt.Fprint(w, `{"success":false,"errors":[{"message":"fixture failure"}]}`)
				return
			}
			result := []cfRecord{}
			if rec, ok := records[name+"|"+typ]; ok {
				result = append(result, rec)
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
			return
		}
		var rec cfRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			t.Error(err)
		}
		rec.ID = rec.Name + "|" + rec.Type
		records[rec.ID] = rec
		writes++
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rec})
	}))
	defer dns.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v6" {
			fmt.Fprint(w, "2001:db8::9")
		} else {
			fmt.Fprint(w, "203.0.113.9")
		}
	}))
	defer source.Close()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task := config.DDNSTask{ID: "task", Name: "dual", ProviderID: "cf", ForceInterval: 30,
		IPv4Source: &config.IPSourceConfig{Source: "api", APIURL: source.URL},
		IPv6Source: &config.IPSourceConfig{Source: "api", APIURL: source.URL + "/v6"},
		Records:    []config.DDNSRecord{{ID: "a", Enabled: true, Domain: "home.example.com", Type: "A", TTL: 300}, {ID: "aaaa", Enabled: true, Domain: "home.example.com", Type: "AAAA", TTL: 300}}}
	save := func() {
		t.Helper()
		if err := cfg.Update(func(c *config.Config) error {
			c.Providers = []config.DNSProviderConf{{ID: "cf", Type: "cloudflare", Key: "fixture", Endpoint: dns.URL}}
			c.DDNS = []config.DDNSTask{task}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	worker := NewWorker(cfg)
	run := func(force bool) {
		t.Helper()
		if err := worker.runTask(context.Background(), task, force); err != nil {
			t.Fatal(err)
		}
	}
	run(false)
	run(false)
	mu.Lock()
	gotWrites, gotQueries := writes, queries
	mu.Unlock()
	if gotWrites != 2 || gotQueries != 2 {
		t.Fatalf("unchanged records queried/wrote remotely: writes=%d queries=%d", gotWrites, gotQueries)
	}
	task.Records[0].Domain = "other.example.com"
	task.Records[0].TTL = 600
	save()
	run(false)
	mu.Lock()
	got := records["other.example.com|A"]
	mu.Unlock()
	if got.Content != "203.0.113.9" || got.TTL != 600 {
		t.Fatalf("same IP configuration edit ignored: %+v", got)
	}
	mu.Lock()
	rec := records["other.example.com|A"]
	rec.Content = "198.51.100.1"
	records[rec.ID] = rec
	mu.Unlock()
	worker.smu.Lock()
	key := "task|a"
	cache := worker.cache[key]
	cache.Checked = time.Now().Add(-time.Minute)
	worker.cache[key] = cache
	worker.smu.Unlock()
	run(false)
	mu.Lock()
	got = records["other.example.com|A"]
	failV6 = true
	mu.Unlock()
	if got.Content != "203.0.113.9" {
		t.Fatal("forced interval failed to repair drift")
	}
	if err := worker.runTask(context.Background(), task, true); err == nil {
		t.Fatal("partial remote failure lost")
	}
	st := worker.Status(task.ID)
	if st.Success || len(st.Records) != 2 || !st.Records[0].Success || st.Records[1].Success {
		t.Fatalf("partial status: %+v", st)
	}
	if len(worker.History(task.ID)) == 0 || worker.History(task.ID)[0].Kind != "failed" {
		t.Fatal("failed history missing")
	}
}

func TestTTLOnlyUpdateThreeProviders(t *testing.T) {
	for _, typ := range []string{"aliyun", "cloudflare", "dnspod"} {
		t.Run(typ, func(t *testing.T) {
			updated := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch typ {
				case "aliyun":
					q := r.URL.Query()
					if q.Get("Action") == "DescribeDomainRecords" {
						fmt.Fprint(w, `{"DomainRecords":{"Record":[{"RecordId":"1","RR":"home","Type":"A","Value":"203.0.113.9","TTL":300}]}}`)
					} else {
						updated = true
						if q.Get("TTL") != "600" {
							t.Error("missing TTL")
						}
						fmt.Fprint(w, `{"RecordId":"1"}`)
					}
				case "cloudflare":
					if r.URL.Path == "/zones" {
						fmt.Fprint(w, `{"success":true,"result":[{"id":"z"}]}`)
					} else if r.Method == "GET" {
						fmt.Fprint(w, `{"success":true,"result":[{"id":"1","name":"home.example.com","type":"A","content":"203.0.113.9","ttl":300,"proxied":true}]}`)
					} else {
						updated = true
						var b cfRecord
						json.NewDecoder(r.Body).Decode(&b)
						if b.TTL != 600 || !b.Proxied {
							t.Errorf("remote options lost: %+v", b)
						}
						fmt.Fprint(w, `{"success":true,"result":{}}`)
					}
				case "dnspod":
					r.ParseForm()
					if r.URL.Path == "/Record.List" {
						fmt.Fprint(w, `{"status":{"code":"1"},"records":[{"id":"1","name":"home","type":"A","value":"203.0.113.9","ttl":"300","line":"电信"}]}`)
					} else {
						updated = true
						if r.Form.Get("ttl") != "600" || r.Form.Get("record_line") != "电信" {
							t.Error("TTL/line lost")
						}
						fmt.Fprint(w, `{"status":{"code":"1"}}`)
					}
				}
			}))
			defer srv.Close()
			p, err := NewProvider(config.DNSProviderConf{Type: typ, Key: "fixture", Secret: "fixture", Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.UpsertRecord(context.Background(), "home.example.com", "A", "203.0.113.9", 600); err != nil {
				t.Fatal(err)
			}
			if !updated {
				t.Fatal("TTL-only update skipped")
			}
		})
	}
}

func TestTaskActionsRequireCurrentRevisionAndRejectDowngrade(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task := config.DDNSTask{ID: "task", Name: "fixture", Interval: 300, ProviderID: "p", IPv4Source: &config.IPSourceConfig{Source: "interface", Interface: "auto"}, Records: []config.DDNSRecord{{ID: "record", Domain: "home.example.com", Type: "A", Enabled: false}}}
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = []config.DNSProviderConf{{ID: "p", Type: "cloudflare", Key: "fixture"}}
		c.DDNS = []config.DDNSTask{task}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(cfg)
	t.Cleanup(worker.Stop)
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, worker)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for _, action := range []string{"toggle", "delete"} {
		path := "/api/ddns/tasks/task"
		method := "DELETE"
		if action == "toggle" {
			path += "/toggle"
			method = "POST"
		}
		if rec := call(method, path, map[string]string{"revision": "stale"}); rec.Code != 409 {
			t.Fatalf("stale %s = %d", action, rec.Code)
		}
	}
	downgrade := map[string]any{"revision": taskRevision(task), "name": "downgrade", "records": []any{}, "domains": []string{"other.example.com"}, "ipType": "ipv4", "ipSource": "interface", "providerId": "p"}
	if rec := call("PUT", "/api/ddns/tasks/task", downgrade); rec.Code != 400 {
		t.Fatalf("downgrade=%d: %s", rec.Code, rec.Body.String())
	}
	delete(downgrade, "revision")
	if rec := call("PUT", "/api/ddns/tasks/task", downgrade); rec.Code != 409 {
		t.Fatalf("missing revision=%d", rec.Code)
	}
	cfg.RLock()
	current := cfg.DDNS[0]
	cfg.RUnlock()
	if current.Enabled || len(current.Records) != 1 {
		t.Fatal("rejected mutation changed config")
	}
	// All records are stopped: exercising toggle never calls external DNS.
	rec := call("POST", "/api/ddns/tasks/task/toggle", map[string]string{"revision": taskRevision(task)})
	if rec.Code != 200 {
		t.Fatalf("valid toggle=%d", rec.Code)
	}
	if rec := call("POST", "/api/ddns/tasks/task/toggle", map[string]string{"revision": taskRevision(task)}); rec.Code != 409 {
		t.Fatal("previous toggle version reused")
	}
	cfg.RLock()
	current = cfg.DDNS[0]
	cfg.RUnlock()
	if rec := call("DELETE", "/api/ddns/tasks/task", map[string]string{"revision": taskRevision(current)}); rec.Code != 200 {
		t.Fatalf("valid delete=%d", rec.Code)
	}
}
func TestPruneRemovesDeletedRecordCache(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Lock()
	cfg.DDNS = []config.DDNSTask{{ID: "task", Records: []config.DDNSRecord{{ID: "kept"}}}}
	cfg.Unlock()
	worker := NewWorker(cfg)
	worker.cache["task|removed"] = recordCache{IP: "203.0.113.1"}
	worker.cache["task|kept"] = recordCache{IP: "203.0.113.2"}
	worker.prune()
	if len(worker.cache) != 1 || worker.cache["task|kept"].IP == "" {
		t.Fatal("record cache leaks on removal")
	}
}
func TestLegacyTaskActionsRemainCompatible(t *testing.T) {
	if err := checkTaskActionRevision(config.DDNSTask{ID: "old", Domains: []string{"home.example.com"}}, ""); err != nil {
		t.Fatal(err)
	}
}
