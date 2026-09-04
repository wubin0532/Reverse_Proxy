package ddns

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
)

// TaskStatus 任务最近一次运行状态。
type TaskStatus struct {
	TaskID   string    `json:"taskId"`
	IP       string    `json:"ip"`
	Success  bool      `json:"success"`
	Message  string    `json:"message"`
	LastTime time.Time `json:"lastTime"`
}

// Worker DDNS 调度器，每个启用的任务一个 goroutine。
type Worker struct {
	cfg *config.Config

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	smu    sync.Mutex
	lastIP map[string]string
	status map[string]*TaskStatus
}

func NewWorker(cfg *config.Config) *Worker {
	return &Worker{
		cfg:    cfg,
		lastIP: make(map[string]string),
		status: make(map[string]*TaskStatus),
	}
}

// Start 启动所有已启用任务的调度。
func (w *Worker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.cfg.RLock()
	tasks := make([]config.DDNSTask, 0, len(w.cfg.DDNS))
	for _, t := range w.cfg.DDNS {
		if t.Enabled {
			tasks = append(tasks, t)
		}
	}
	w.cfg.RUnlock()
	for _, t := range tasks {
		w.wg.Add(1)
		go w.loop(ctx, t)
	}
	log.Printf("[DDNS] 调度器已启动，共 %d 个任务", len(tasks))
}

// Stop 停止全部任务并等待退出。
func (w *Worker) Stop() {
	w.mu.Lock()
	cancel := w.cancel
	w.cancel = nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
}

// Reload 任务配置变更后重启调度。
func (w *Worker) Reload() {
	w.Stop()
	w.Start()
}

// Status 查询单个任务状态，未运行过返回 nil。
func (w *Worker) Status(taskID string) *TaskStatus {
	w.smu.Lock()
	defer w.smu.Unlock()
	if st, ok := w.status[taskID]; ok {
		cp := *st
		return &cp
	}
	return nil
}

// RunNow 手动立即执行一次任务，同步返回结果。
func (w *Worker) RunNow(taskID string) error {
	w.cfg.RLock()
	var task *config.DDNSTask
	for i := range w.cfg.DDNS {
		if w.cfg.DDNS[i].ID == taskID {
			t := w.cfg.DDNS[i]
			task = &t
			break
		}
	}
	w.cfg.RUnlock()
	if task == nil {
		return fmt.Errorf("任务不存在")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return w.runTask(ctx, *task, true)
}

func (w *Worker) loop(ctx context.Context, task config.DDNSTask) {
	defer w.wg.Done()
	if err := w.runTask(ctx, task, false); err != nil {
		log.Printf("[DDNS] 任务 %s 首次执行失败: %v", task.Name, err)
	}
	interval := task.Interval
	if interval <= 0 {
		interval = 60
	}
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
			if err := w.runTask(ctx, task, false); err != nil {
				log.Printf("[DDNS] 任务 %s 执行失败: %v", task.Name, err)
			}
		}
	}
}

// setStatus 记录最近一次运行状态。
func (w *Worker) setStatus(taskID, ip string, success bool, msg string) {
	w.smu.Lock()
	defer w.smu.Unlock()
	w.status[taskID] = &TaskStatus{
		TaskID:   taskID,
		IP:       ip,
		Success:  success,
		Message:  msg,
		LastTime: time.Now(),
	}
}

func (w *Worker) providerFor(providerID string) (Provider, error) {
	w.cfg.RLock()
	var conf *config.DNSProviderConf
	for i := range w.cfg.Providers {
		if w.cfg.Providers[i].ID == providerID {
			c := w.cfg.Providers[i]
			conf = &c
			break
		}
	}
	w.cfg.RUnlock()
	if conf == nil {
		return nil, fmt.Errorf("服务商凭据不存在: %s", providerID)
	}
	return NewProvider(*conf)
}

// runTask 执行一次任务。force 为 true 时忽略 IP 缓存强制更新。
func (w *Worker) runTask(ctx context.Context, task config.DDNSTask, force bool) error {
	ip, err := GetIP(ctx, task)
	if err != nil {
		w.setStatus(task.ID, "", false, err.Error())
		return err
	}
	cacheKey := task.ID + "|" + task.IPType
	w.smu.Lock()
	changed := w.lastIP[cacheKey] != ip
	if changed {
		w.lastIP[cacheKey] = ip
	}
	w.smu.Unlock()
	if !changed && !force {
		w.setStatus(task.ID, ip, true, "IP 未变化，跳过更新")
		return nil
	}

	recordType := "A"
	if task.IPType == "ipv6" {
		recordType = "AAAA"
	}
	provider, err := w.providerFor(task.ProviderID)
	if err != nil {
		w.setStatus(task.ID, ip, false, err.Error())
		return err
	}
	var msgs []string
	for _, d := range task.Domains {
		msg, err := provider.UpsertRecord(ctx, d, recordType, ip, task.TTL)
		if err != nil {
			w.setStatus(task.ID, ip, false, fmt.Sprintf("%s: %v", d, err))
			return fmt.Errorf("更新 %s 失败: %w", d, err)
		}
		log.Printf("[DDNS] 任务 %s: %s", task.Name, msg)
		msgs = append(msgs, msg)
	}
	w.setStatus(task.ID, ip, true, strings.Join(msgs, "；"))
	return nil
}
