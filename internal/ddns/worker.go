package ddns

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/notify"
)

var errTaskChanged = errors.New("任务配置已变更，已丢弃旧运行结果")

type RecordStatus struct {
	RecordID      string    `json:"recordId"`
	Domain        string    `json:"domain"`
	Type          string    `json:"type"`
	IP            string    `json:"ip"`
	Interface     string    `json:"interface,omitempty"`
	Success       bool      `json:"success"`
	Message       string    `json:"message"`
	LastTime      time.Time `json:"lastTime"`
	RemoteChecked bool      `json:"remoteChecked"`
	Changed       bool      `json:"changed"`
}

type TaskStatus struct {
	TaskID    string         `json:"taskId"`
	IP        string         `json:"ip"`
	Interface string         `json:"interface,omitempty"`
	Success   bool           `json:"success"`
	Message   string         `json:"message"`
	LastTime  time.Time      `json:"lastTime"`
	Records   []RecordStatus `json:"records,omitempty"`
}

type HistoryEntry struct {
	Time       time.Time `json:"time"`
	RecordID   string    `json:"recordId"`
	Domain     string    `json:"domain"`
	Type       string    `json:"type"`
	Kind       string    `json:"kind"` // changed, synced, failed
	IP         string    `json:"ip"`
	PreviousIP string    `json:"previousIp,omitempty"`
	Message    string    `json:"message"`
}

type recordCache struct {
	IP, Fingerprint string
	Checked         time.Time
}

// Worker serializes lifecycle changes and each task's execution. Config locks
// cover an individual provider operation, so an edit cannot overtake that write.
type Worker struct {
	cfg     *config.Config
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	wg      sync.WaitGroup
	smu     sync.Mutex
	lastIP  map[string]string
	status  map[string]*TaskStatus
	cache   map[string]recordCache
	history map[string][]HistoryEntry
	taskMu  map[string]*sync.Mutex
}

func NewWorker(cfg *config.Config) *Worker {
	return &Worker{cfg: cfg, lastIP: map[string]string{}, status: map[string]*TaskStatus{}, cache: map[string]recordCache{}, history: map[string][]HistoryEntry{}, taskMu: map[string]*sync.Mutex{}}
}
func (w *Worker) lockTask(id string) *sync.Mutex {
	w.smu.Lock()
	m := w.taskMu[id]
	if m == nil {
		m = &sync.Mutex{}
		w.taskMu[id] = m
	}
	w.smu.Unlock()
	m.Lock()
	return m
}
func (w *Worker) lifecycleLocked() {
	if w.cancel == nil {
		w.ctx, w.cancel = context.WithCancel(context.Background())
	}
}
func (w *Worker) Start() { w.mu.Lock(); defer w.mu.Unlock(); w.startLocked() }
func (w *Worker) startLocked() {
	if w.started {
		return
	}
	w.lifecycleLocked()
	w.started = true
	w.cfg.RLock()
	var tasks []config.DDNSTask
	for _, task := range w.cfg.DDNS {
		if task.Enabled {
			tasks = append(tasks, task)
		}
	}
	w.cfg.RUnlock()
	for _, task := range tasks {
		w.wg.Add(1)
		go w.loop(w.ctx, task)
	}
	log.Printf("[DDNS] 调度器已启动，共 %d 个任务", len(tasks))
}
func (w *Worker) Stop() { w.mu.Lock(); defer w.mu.Unlock(); w.stopLocked() }
func (w *Worker) stopLocked() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	w.ctx, w.cancel, w.started = nil, nil, false
}
func (w *Worker) Reload() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	w.prune()
	w.startLocked()
}
func (w *Worker) prune() {
	w.cfg.RLock()
	tasks, records := map[string]bool{}, map[string]bool{}
	for _, task := range w.cfg.DDNS {
		tasks[task.ID] = true
		for _, record := range effectiveRecords(task) {
			records[task.ID+"|"+record.ID] = true
		}
	}
	w.cfg.RUnlock()
	w.smu.Lock()
	defer w.smu.Unlock()
	// Reload has already waited for all executions, so retired task locks are safe to discard.
	for id := range w.status {
		delete(w.status, id)
	}
	for id := range w.history {
		if !tasks[id] {
			delete(w.history, id)
		}
	}
	for id := range w.taskMu {
		if !tasks[id] {
			delete(w.taskMu, id)
		}
	}
	for key := range w.cache {
		if !records[key] {
			delete(w.cache, key)
		}
	}
	for key := range w.lastIP {
		if !tasks[strings.SplitN(key, "|", 2)[0]] {
			delete(w.lastIP, key)
		}
	}
}

func (w *Worker) Status(id string) *TaskStatus {
	w.smu.Lock()
	defer w.smu.Unlock()
	if st := w.status[id]; st != nil {
		out := *st
		out.Records = append([]RecordStatus(nil), st.Records...)
		return &out
	}
	return nil
}
func (w *Worker) History(id string) []HistoryEntry {
	w.smu.Lock()
	defer w.smu.Unlock()
	entries := w.history[id]
	out := make([]HistoryEntry, len(entries))
	for i := range entries {
		out[i] = entries[len(entries)-i-1]
	}
	return out
}
func (w *Worker) RunNow(id string) error {
	w.mu.Lock()
	w.lifecycleLocked()
	ctx, cancel := context.WithTimeout(w.ctx, 60*time.Second)
	w.wg.Add(1)
	w.mu.Unlock()
	defer w.wg.Done()
	defer cancel()
	w.cfg.RLock()
	var task config.DDNSTask
	found := false
	for _, current := range w.cfg.DDNS {
		if current.ID == id {
			task = current
			found = true
			break
		}
	}
	w.cfg.RUnlock()
	if !found {
		return errTaskNotFound
	}
	return w.runTask(ctx, task, true)
}
func (w *Worker) loop(ctx context.Context, task config.DDNSTask) {
	defer w.wg.Done()
	run := func() {
		if err := w.runTask(ctx, task, false); err != nil && ctx.Err() == nil && !errors.Is(err, errTaskChanged) {
			log.Printf("[DDNS] 任务 %s 执行失败: %v", task.Name, err)
		}
	}
	run()
	interval := task.Interval
	if interval < 30 {
		interval = 30
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// Caller holds cfg.RLock. The private fingerprint detects credential changes
// as well as domains, TTL, sources, flags and task identity.
func (w *Worker) matchesLocked(task config.DDNSTask, provider config.DNSProviderConf) bool {
	for _, current := range w.cfg.DDNS {
		if current.ID != task.ID {
			continue
		}
		for _, saved := range w.cfg.Providers {
			if saved.ID == current.ProviderID {
				return targetFingerprint(current, saved) == targetFingerprint(task, provider)
			}
		}
	}
	return false
}
func (w *Worker) snapshotProvider(task config.DDNSTask) (config.DNSProviderConf, error) {
	w.cfg.RLock()
	defer w.cfg.RUnlock()
	for _, provider := range w.cfg.Providers {
		if provider.ID == task.ProviderID {
			if !w.matchesLocked(task, provider) {
				return config.DNSProviderConf{}, errTaskChanged
			}
			return provider, nil
		}
	}
	return config.DNSProviderConf{}, fmt.Errorf("服务商凭据不存在")
}
func (w *Worker) runTask(ctx context.Context, task config.DDNSTask, force bool) error {
	lock := w.lockTask(task.ID)
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	providerConf, err := w.snapshotProvider(task)
	if err != nil {
		return err
	}
	provider, err := NewProvider(providerConf)
	if err != nil {
		return err
	}
	fingerprint := targetFingerprint(task, providerConf)
	forceInterval := task.ForceInterval
	if forceInterval <= 0 {
		forceInterval = defaultForceInterval
	}
	now := time.Now()
	status := &TaskStatus{TaskID: task.ID, Success: true, LastTime: now}
	var entries []HistoryEntry
	var firstErr error
	type ipResult struct {
		ip, iface string
		err       error
	}
	sources := map[string]ipResult{}
	for _, record := range effectiveRecords(task) {
		if !record.Enabled {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		ip, ok := sources[record.Type]
		if record.Type == "CNAME" || record.Type == "TXT" {
			ip = ipResult{ip: record.Value}
		} else if !ok {
			ip.ip, ip.iface, ip.err = GetIPDetail(ctx, sourceTask(task, record.Type))
			sources[record.Type] = ip
		}
		rs := RecordStatus{RecordID: record.ID, Domain: record.Domain, Type: record.Type, IP: ip.ip, Interface: ip.iface, LastTime: now, Success: ip.err == nil}
		key := task.ID + "|" + record.ID
		w.smu.Lock()
		previous := w.cache[key]
		w.smu.Unlock()
		if ip.err != nil {
			rs.Message = ip.err.Error()
			if firstErr == nil {
				firstErr = ip.err
			}
		} else {
			if status.IP == "" {
				status.IP, status.Interface = ip.ip, ip.iface
			}
			due := force || previous.Fingerprint != fingerprint || previous.IP != ip.ip || previous.Checked.IsZero() || now.Sub(previous.Checked) >= time.Duration(forceInterval)*time.Second
			if !due {
				rs.Message = "记录值与配置未变化，跳过更新"
			} else {
				w.cfg.RLock()
				if !w.matchesLocked(task, providerConf) {
					w.cfg.RUnlock()
					return errTaskChanged
				}
				if err := ctx.Err(); err != nil {
					w.cfg.RUnlock()
					return err
				}
				rs.RemoteChecked = true
				rs.Message, err = provider.UpsertRecord(ctx, record.Domain, record.Type, ip.ip, record.TTL)
				if err == nil {
					rs.Changed = strings.HasPrefix(rs.Message, "新增记录") || strings.HasPrefix(rs.Message, "更新记录")
					w.smu.Lock()
					w.cache[key] = recordCache{IP: ip.ip, Fingerprint: fingerprint, Checked: now}
					w.smu.Unlock()
				} else {
					rs.Success = false
					rs.Message = err.Error()
					if firstErr == nil {
						firstErr = err
					}
				}
				w.cfg.RUnlock()
			}
		}
		if !rs.Success {
			status.Success = false
		}
		status.Records = append(status.Records, rs)
		if rs.RemoteChecked || !rs.Success {
			kind := "synced"
			if rs.Changed {
				kind = "changed"
			}
			if !rs.Success {
				kind = "failed"
			}
			entries = append(entries, HistoryEntry{Time: now, RecordID: record.ID, Domain: record.Domain, Type: record.Type, Kind: kind, IP: ip.ip, PreviousIP: previous.IP, Message: rs.Message})
		}
	}
	if len(status.Records) == 0 {
		status.Message = "没有启用的记录"
	} else {
		var messages []string
		for _, rs := range status.Records {
			messages = append(messages, rs.Domain+" "+rs.Type+": "+rs.Message)
		}
		status.Message = strings.Join(messages, "；")
	}
	w.cfg.RLock()
	defer w.cfg.RUnlock()
	if !w.matchesLocked(task, providerConf) {
		return errTaskChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.smu.Lock()
	w.status[task.ID] = status
	history := append(w.history[task.ID], entries...)
	if len(history) > 100 {
		history = append([]HistoryEntry(nil), history[len(history)-100:]...)
	}
	w.history[task.ID] = history
	if status.Success {
		w.lastIP[task.ID+"|"+task.IPType] = status.IP
	}
	w.smu.Unlock()
	if firstErr != nil {
		notify.Publish(notify.Event{Type: notify.TypeDDNSUpdateFailed, Entity: task.Name, Level: notify.LevelError, Message: fmt.Sprintf("DDNS 任务 %s 更新失败: %s", task.Name, firstErr.Error())})
	}
	return firstErr
}
