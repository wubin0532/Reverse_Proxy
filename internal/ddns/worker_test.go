package ddns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func TestFailedUpdateDoesNotAdvanceIPCache(t *testing.T) {
	ipServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.9"))
	}))
	defer ipServer.Close()
	failingProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary failure", http.StatusServiceUnavailable)
	}))
	defer failingProvider.Close()

	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	providerID := "cf-test"
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = []config.DNSProviderConf{{ID: providerID, Type: "cloudflare", Key: "token", Endpoint: failingProvider.URL}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task := config.DDNSTask{ID: "task-1", Name: "test", Domains: []string{"www.example.com"}, IPType: "ipv4", IPSource: "api", APIURL: ipServer.URL, ProviderID: providerID}
	worker := NewWorker(cfg)
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = worker.runTask(ctx, task, false)
		cancel()
		if err == nil {
			t.Fatal("expected provider failure")
		}
	}
	worker.smu.Lock()
	_, cached := worker.lastIP[task.ID+"|"+task.IPType]
	worker.smu.Unlock()
	if cached {
		t.Fatal("failed DNS update must not advance IP cache")
	}
}

func TestConcurrentReloadsAreSerialized(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(cfg)
	worker.Start()
	defer worker.Stop()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Reload()
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent reloads deadlocked")
	}
}
