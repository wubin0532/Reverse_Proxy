package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

const maxHistoryBytes = 2 << 20
const historyVersion = 1

type diskHistory struct {
	Version  int     `json:"version"`
	Realtime []Point `json:"realtime"`
	Minutes  []Point `json:"minutes"`
}

func (s *Sampler) ConfigurePersistence() {
	if s.cfg == nil {
		return
	}
	s.cfg.RLock()
	enabled := s.cfg.Settings.MetricsHistory
	s.cfg.RUnlock()
	s.mu.Lock()
	s.persistent = enabled
	s.mu.Unlock()
}
func (s *Sampler) Storage() (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.persistent {
		return "disk", s.persistError
	}
	return "memory", s.persistError
}
func validHistory(points []Point, limit int, now time.Time) bool {
	if len(points) > limit {
		return false
	}
	var previous time.Time
	for _, p := range points {
		if p.Time.IsZero() || p.Time.After(now.Add(time.Minute)) || (!previous.IsZero() && !p.Time.After(previous)) {
			return false
		}
		previous = p.Time
		for i, v := range []*float64{p.CPU, p.Memory, p.BytesInPerSecond, p.BytesOutPerSecond} {
			if v == nil {
				continue
			}
			if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 1e15 || i < 2 && *v > 100 {
				return false
			}
		}
	}
	return true
}
func (s *Sampler) loadHistory() {

	root, err := os.OpenRoot(s.cfg.Dir())
	if err != nil {
		s.persistError = "历史目录无法读取"
		s.persistBlocked = true
		return
	}
	defer root.Close()
	f, err := root.Open("metrics-history.json")
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		s.persistError = "历史文件无法读取"
		s.persistBlocked = true
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxHistoryBytes+1))
	var history diskHistory
	if err != nil || len(data) > maxHistoryBytes || json.Unmarshal(data, &history) != nil || history.Version != historyVersion || !validHistory(history.Realtime, 720, time.Now()) || !validHistory(history.Minutes, 1440, time.Now()) {
		s.persistError = "历史文件无效，已保留异常副本"
		if root.Rename("metrics-history.json", "metrics-history.json.invalid") != nil {
			s.persistError = "历史文件无效，异常副本保留失败"
			s.persistBlocked = true
		}
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, p := range history.Realtime {
		if p.Time.After(cutoff) {
			s.realtime = append(s.realtime, p)
		}
	}
	for _, p := range history.Minutes {
		if p.Time.After(cutoff) {
			s.minutes = append(s.minutes, p)
		}
	}
	if len(s.minutes) > 0 {
		s.minute = s.minutes[len(s.minutes)-1].Time.Truncate(time.Minute)
	}
}
func (s *Sampler) flushHistory(force bool) {
	if s.cfg == nil {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	now := time.Now()
	s.mu.Lock()
	if !s.persistent || s.persistBlocked || !force && now.Sub(s.lastFlush) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastFlush = now
	history := diskHistory{Version: historyVersion, Realtime: append([]Point(nil), s.realtime...), Minutes: append([]Point(nil), s.minutes...)}
	s.mu.Unlock()
	data, err := json.Marshal(history)
	if err == nil && len(data) > maxHistoryBytes {
		err = fmt.Errorf("历史超出容量")
	}
	if err == nil {
		err = writeHistory(s.cfg.Dir(), data)
	}
	s.mu.Lock()
	if err == nil {
		s.persistError = ""
	} else {
		s.persistError = "历史写入失败，当前采样仍保留在内存"
	}
	s.mu.Unlock()
}
func writeHistory(dir string, data []byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	name := ".metrics-history-" + ids.New()
	temp, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = root.Rename(name, "metrics-history.json"); err != nil {
		return err
	}
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *Sampler) registerHistorySettings(r chi.Router) {
	r.Get("/api/metrics/history/settings", func(w http.ResponseWriter, q *http.Request) {
		storage, err := s.Storage()
		api.OK(w, map[string]any{"enabled": storage == "disk", "error": err, "maxBytes": maxHistoryBytes, "flushSeconds": 60, "realtimeCapacity": 720, "minuteCapacity": 1440})
	})
	r.Post("/api/metrics/history/settings", func(w http.ResponseWriter, q *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := api.DecodeBody(q, &body); err != nil {
			api.FailDecode(w, err, "请求格式错误")
			return
		}
		if s.cfg == nil {
			api.Fail(w, 400, "未配置持久目录")
			return
		}
		if err := s.cfg.Update(func(c *config.Config) error { c.Settings.MetricsHistory = body.Enabled; return nil }); err != nil {
			api.Fail(w, 500, "保存失败")
			return
		}
		s.ConfigurePersistence()
		s.flushHistory(true)
		api.OK(w, map[string]bool{"enabled": body.Enabled})
	})
}
