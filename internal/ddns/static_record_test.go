package ddns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"andey-proxy/internal/config"
)

// Every provider goes through actual HTTP requests for create, no-op, edit,
// drift repair and refusal to modify ambiguous remote records.
func TestStaticRecordsThreeProviders(t *testing.T) {
	for _, providerType := range []string{"cloudflare", "aliyun", "dnspod"} {
		for _, recordType := range []string{"CNAME", "TXT"} {
			t.Run(providerType+"/"+recordType, func(t *testing.T) {
				var mu sync.Mutex
				value := ""
				ttl := 0
				writes := 0
				ambiguous := false
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					switch providerType {
					case "cloudflare":
						if r.URL.Path == "/zones" {
							fmt.Fprint(w, `{"success":true,"result":[{"id":"zone"}]}`)
							return
						}
						if r.Method == "GET" {
							rows := []cfRecord{}
							if value != "" {
								rows = append(rows, cfRecord{ID: "record", Name: "entry.example.com", Type: recordType, Content: value, TTL: ttl, Proxied: recordType == "CNAME"})
							}
							if ambiguous {
								rows = append(rows, rows[0])
							}
							json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
							return
						}
						var body map[string]any
						json.NewDecoder(r.Body).Decode(&body)
						if writes > 0 && r.Method != "PATCH" {
							t.Error("update must preserve unrelated metadata with PATCH")
						}
						if body["type"] != recordType || body["name"] != "entry.example.com" {
							t.Error("wrong target")
						}
						if writes > 0 && recordType == "CNAME" && body["proxied"] != true {
							t.Error("proxy mode changed")
						}
						if recordType == "TXT" && body["proxied"] != nil {
							t.Error("TXT proxy flag sent")
						}
						value = body["content"].(string)
						ttl = int(body["ttl"].(float64))
						writes++
						fmt.Fprint(w, `{"success":true}`)
					case "aliyun":
						q := r.URL.Query()
						if q.Get("Action") == "DescribeDomainRecords" {
							rows := []aliyunRecord{}
							if value != "" {
								rows = append(rows, aliyunRecord{RecordID: "record", RR: "entry", Type: recordType, Value: value, TTL: ttl})
							}
							if ambiguous {
								rows = append(rows, rows[0])
							}
							json.NewEncoder(w).Encode(map[string]any{"DomainRecords": map[string]any{"Record": rows}})
							return
						}
						if q.Get("Type") != recordType {
							t.Error("type lost")
						}
						value = q.Get("Value")
						fmt.Sscanf(q.Get("TTL"), "%d", &ttl)
						writes++
						fmt.Fprint(w, `{"RecordId":"record"}`)
					case "dnspod":
						r.ParseForm()
						if r.URL.Path == "/Record.List" {
							if value == "" {
								fmt.Fprint(w, `{"status":{"code":"10","message":"empty"}}`)
								return
							}
							rows := []dnspodRecord{{ID: "1", Name: "entry", Type: recordType, Value: value, TTL: json.Number(fmt.Sprint(ttl)), Line: "电信"}}
							if ambiguous {
								rows = append(rows, rows[0])
							}
							json.NewEncoder(w).Encode(map[string]any{"status": map[string]string{"code": "1"}, "records": rows})
							return
						}
						if r.Form.Get("record_type") != recordType {
							t.Error("type lost")
						}
						if writes > 0 && r.Form.Get("record_line") != "电信" {
							t.Error("line lost")
						}
						value = r.Form.Get("value")
						fmt.Sscanf(r.Form.Get("ttl"), "%d", &ttl)
						writes++
						fmt.Fprint(w, `{"status":{"code":"1"}}`)
					}
				}))
				defer server.Close()
				cfg, err := config.Load(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				literal := "target.example.com"
				if recordType == "TXT" {
					literal = "  v=spf1 include:example.com ~all \"quoted\"  "
				}
				task := config.DDNSTask{ID: "static", Name: "static", ProviderID: "provider", Interval: 300, Records: []config.DDNSRecord{{ID: "record", Enabled: true, Domain: "entry.example.com", Type: recordType, Value: literal, TTL: 300}}}
				save := func() {
					t.Helper()
					if err := cfg.Update(func(c *config.Config) error {
						c.Providers = []config.DNSProviderConf{{ID: "provider", Type: providerType, Key: "fixture", Secret: "fixture", Endpoint: server.URL}}
						c.DDNS = []config.DDNSTask{task}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				save()
				worker := NewWorker(cfg)
				defer worker.Stop()
				if err := worker.RunNow(task.ID); err != nil {
					t.Fatal(err)
				}
				if err := worker.runTask(context.Background(), task, false); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				got, gotWrites := value, writes
				mu.Unlock()
				if got != literal || gotWrites != 1 {
					t.Fatal("literal altered or no-op wrote again", got, gotWrites)
				}
				task.Records[0].Value = literal + "updated"
				if recordType == "CNAME" {
					task.Records[0].Value = "updated.example.com"
				}
				task.Records[0].TTL = 600
				save()
				if err := worker.RunNow(task.ID); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				if value != task.Records[0].Value || ttl != 600 {
					t.Error("edit not reconciled")
				}
				value = literal
				mu.Unlock()
				if err := worker.RunNow(task.ID); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				before := writes
				ambiguous = true
				mu.Unlock()
				if err := worker.RunNow(task.ID); err == nil {
					t.Fatal("ambiguous record overwritten")
				}
				mu.Lock()
				after := writes
				mu.Unlock()
				if before != after {
					t.Fatal("ambiguous write occurred")
				}
				if worker.Status(task.ID).Success || worker.History(task.ID)[0].Kind != "failed" {
					t.Fatal("ambiguous failure not visible")
				}
				loaded, err := config.Load(cfg.Dir())
				if err != nil || loaded.DDNS[0].Records[0].Value != task.Records[0].Value {
					t.Fatal("static value not persisted", err)
				}
			})
		}
	}
}

func TestStaticValidationDoesNotRequireIPSource(t *testing.T) {
	for _, tt := range []struct {
		typ, value string
		ok         bool
	}{{"CNAME", " TARGET.Example.COM. ", true}, {"CNAME", "203.0.113.9", false}, {"CNAME", "https://example.com", false}, {"CNAME", "entry.example.com", false}, {"TXT", "  Preserve Case  ", true}, {"TXT", "", false}, {"TXT", "line\nline", false}, {"TXT", strings.Repeat("x", 2049), false}} {
		task := config.DDNSTask{Name: "static", Records: []config.DDNSRecord{{Domain: "entry.example.com", Type: tt.typ, Value: tt.value}}}
		code, _ := validateTaskShape(&task)
		if (code == 0) != tt.ok {
			t.Errorf("%s validation incorrect: %d", tt.typ, code)
		}
		if tt.ok && tt.typ == "TXT" && task.Records[0].Value != tt.value {
			t.Fatal("TXT trimmed")
		}
	}
}
