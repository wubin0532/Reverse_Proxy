package metrics

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func TestPersistentHistorySurvivesSamplerRestart(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	if err := cfg.Update(func(c *config.Config) error { c.Settings.MetricsHistory = true; return nil }); err != nil {
		t.Fatal(err)
	}
	s := &Sampler{cfg: cfg, persistent: true, stop: make(chan struct{})}
	at := time.Now().Add(-5 * time.Minute)
	s.sample(at, hostCounters{total: 100, idle: 50, cpuOK: true})
	s.sample(at.Add(5*time.Second), hostCounters{total: 200, idle: 100, cpuOK: true})
	s.Stop()
	path := filepath.Join(cfg.Dir(), "metrics-history.json")
	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0o600 || stat.Size() > maxHistoryBytes {
		t.Fatal(stat, err)
	}
	restarted := NewSampler(cfg)
	defer restarted.Stop()
	points := restarted.History(false)
	found := false
	for _, point := range points {
		if point.Time.Equal(at.Add(5 * time.Second)) {
			found = point.CPU != nil && *point.CPU == 50
		}
	}
	if !found {
		t.Fatal("old measured sample lost across restart", points)
	}
	storage, message := restarted.Storage()
	if storage != "disk" || message != "" {
		t.Fatal(storage, message)
	}
}
func TestCorruptHistoryDoesNotBlockStartupOrDisappear(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	cfg.Update(func(c *config.Config) error { c.Settings.MetricsHistory = true; return nil })
	path := filepath.Join(cfg.Dir(), "metrics-history.json")
	invalid := []byte(`{"version":99,"realtime":[],"minutes":[]}`)
	os.WriteFile(path, invalid, 0o600)
	sampler := NewSampler(cfg)
	defer sampler.Stop()
	_, message := sampler.Storage()
	if message == "" || len(sampler.History(false)) == 0 {
		t.Fatal("corruption silently accepted or sampling blocked")
	}
	preserved, err := os.ReadFile(path + ".invalid")
	if err != nil || !bytes.Equal(preserved, invalid) {
		t.Fatal("unsupported format not preserved", err)
	}
}
func TestHistorySettingPersistsAndBackwardClockRemainsValid(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	s := NewSampler(cfg)
	defer s.Stop()
	r := chi.NewRouter()
	RegisterRoutes(r, s)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/metrics/history/settings", bytes.NewBufferString(`{"enabled":true}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cfg.RLock()
	enabled := cfg.Settings.MetricsHistory
	cfg.RUnlock()
	if !enabled {
		t.Fatal("setting not persisted")
	}
	at := time.Now().Add(-time.Minute)
	s.sample(at, hostCounters{})
	s.flushHistory(true)
	data, err := os.ReadFile(filepath.Join(cfg.Dir(), "metrics-history.json"))
	if err != nil {
		t.Fatal(err)
	}
	var history diskHistory
	if json.Unmarshal(data, &history) != nil || !validHistory(history.Realtime, 720, time.Now()) || !validHistory(history.Minutes, 1440, time.Now()) {
		t.Fatal("clock rollback created invalid disk history")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/metrics/history/settings", bytes.NewBufferString(`{"enabled":false}`)))
	storage, _ := s.Storage()
	if w.Code != 200 || storage != "memory" {
		t.Fatal(w.Code, storage)
	}
}

func TestFailedHistoryQuarantineNeverOverwritesUnknownData(t *testing.T) {
	cfg, e := config.Load(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer cfg.State().Close()
	if e = cfg.Update(func(c *config.Config) error { c.Settings.MetricsHistory = true; return nil }); e != nil {
		t.Fatal(e)
	}
	invalid := []byte(`{"version":99,"unknown":"keep"}`)
	p := filepath.Join(cfg.Dir(), "metrics-history.json")
	if e = os.WriteFile(p, invalid, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(p+".invalid", 0700); e != nil {
		t.Fatal(e)
	}
	sampler := NewSampler(cfg)
	sampler.Stop()
	data, e := os.ReadFile(p)
	if e != nil || !bytes.Equal(data, invalid) {
		t.Fatal("failed quarantine overwrote unknown history", e, string(data))
	}
	if _, message := sampler.Storage(); message == "" {
		t.Fatal("quarantine failure hidden")
	}
}
