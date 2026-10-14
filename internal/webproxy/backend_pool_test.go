package webproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func poolFixture(t *testing.T, p config.BackendPool) (*config.Config, *Service, string) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cfg.State().Close() })
	err = cfg.Update(func(c *config.Config) error {
		c.BackendPools = []config.BackendPool{p}
		c.Sites = []config.Site{{ID: "site", Name: "test", Enabled: true, Listen: "127.0.0.1:0", Rules: []config.SubRule{{ID: "a", Name: "a", Type: "reverse", Enabled: true, FrontendPath: "/a", BackendPoolID: p.ID}, {ID: "b", Name: "b", Type: "reverse", Enabled: true, FrontendPath: "/b", BackendPoolID: p.ID}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfg, nil)
	svc.Start()
	t.Cleanup(svc.Stop)
	return cfg, svc, "http://" + svc.ListenAddr("site")
}
func poolGet(t *testing.T, u string) (int, string) {
	t.Helper()
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}
func TestBackendPoolWeightedAndHotUpdate(t *testing.T) {
	one := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "one") }))
	defer one.Close()
	two := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "two") }))
	defer two.Close()
	p := config.BackendPool{ID: "pool", Name: "Pool", Revision: 1, Nodes: []config.BackendNode{{URL: one.URL, Weight: 3, Enabled: true}, {URL: two.URL, Weight: 1, Enabled: true}}}
	cfg, svc, u := poolFixture(t, p)
	counts := map[string]int{}
	for i := 0; i < 20; i++ {
		code, body := poolGet(t, u+"/a")
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		counts[body]++
	}
	if counts["one"] != 15 || counts["two"] != 5 {
		t.Fatal(counts)
	}
	cfg.Update(func(c *config.Config) error { c.BackendPools[0].Nodes[0].Enabled = false; return nil })
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, body := poolGet(t, u+"/a"); body != "two" {
		t.Fatal(body)
	}
	cfg.Update(func(c *config.Config) error { c.BackendPools[0].Nodes[1].Enabled = false; return nil })
	svc.Reload()
	if code, _ := poolGet(t, u+"/a"); code != 503 {
		t.Fatalf("all disabled: %d", code)
	}
}
func TestBackendPoolAdmissionSharedAcrossRulesAndReload(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		io.WriteString(w, "ok")
	}))
	defer backend.Close()
	p := config.BackendPool{ID: "pool", Name: "Pool", Revision: 1, MaxConcurrent: 1, Nodes: []config.BackendNode{{URL: backend.URL, Weight: 1, Enabled: true}}}
	cfg, svc, u := poolFixture(t, p)
	done := make(chan error, 1)
	go func() {
		res, err := http.Get(u + "/a")
		if err == nil {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("backend not reached")
	}
	defer close(release)
	if code, _ := poolGet(t, u+"/b"); code != 503 {
		t.Fatalf("second rule ignored shared limit: %d", code)
	}
	if err := cfg.Update(func(c *config.Config) error { c.BackendPools[0].Nodes[0].Weight = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if code, _ := poolGet(t, u+"/b"); code != 503 {
		t.Fatalf("reload reset inflight count: %d", code)
	}
	// Release before cleanup attempts to stop the service.
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestBackendPoolAPIRevisionReferencesAndBackup(t *testing.T) {
	p := config.BackendPool{ID: "pool", Name: "Pool", Revision: 1, Nodes: []config.BackendNode{{URL: "http://127.0.0.1:1", Weight: 1, Enabled: true}}}
	cfg, svc, _ := poolFixture(t, p)
	r := chi.NewRouter()
	RegisterRoutes(r, cfg, svc)
	req := httptest.NewRequest("DELETE", "/api/backend-pools/pool", nil)
	req.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	p.Revision = 0
	b, _ := json.Marshal(p)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/api/backend-pools/pool", bytes.NewReader(b)))
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	p.Revision = 1
	p.Nodes[0].URL = "http://user:secret@localhost"
	b, _ = json.Marshal(p)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/api/backend-pools/pool", bytes.NewReader(b)))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	before, _ := cfg.PlainJSON()
	err := cfg.Update(func(c *config.Config) error { c.BackendPools = nil; return nil })
	if err == nil {
		t.Fatal("dangling reference accepted")
	}
	after, _ := cfg.PlainJSON()
	if !bytes.Equal(before, after) {
		t.Fatal("failed mutation changed config")
	}
	if !strings.Contains(string(after), `"backendPoolId":"pool"`) {
		t.Fatal("pool reference missing from backup")
	}
}

func TestBackendPoolRetirementReleasesUnusedRuntime(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer backend.Close()
	p := config.BackendPool{ID: "pool", Name: "Pool", Nodes: []config.BackendNode{{URL: backend.URL, Weight: 1, Enabled: true}}}
	cfg, svc, u := poolFixture(t, p)
	for i := 0; i < 20; i++ {
		if e := cfg.Update(func(c *config.Config) error {
			c.BackendPools[0].Nodes[0].URL = backend.URL + "/?version=" + fmt.Sprint(i)
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if e := svc.Reload(); e != nil {
			t.Fatal(e)
		}
		if code, _ := poolGet(t, u+"/a"); code != 200 {
			t.Fatal(code)
		}
		pools, nodes := 0, 0
		svc.poolAdmissions.Range(func(_, v any) bool {
			pools++
			admission := v.(*poolAdmission)
			admission.mu.Lock()
			nodes += len(admission.nodes)
			admission.mu.Unlock()
			return true
		})
		if pools != 1 || nodes != 1 {
			t.Fatal("retired pool runtime retained", pools, nodes)
		}
	}
	svc.Stop()
	count := 0
	svc.poolAdmissions.Range(func(_, v any) bool { count++; return true })
	if count != 0 {
		t.Fatal("stop retained admissions", count)
	}
}
