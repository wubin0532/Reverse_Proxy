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
	TaskID    string    `json:"taskId"`
	IP        string    `json:"ip"`
	Interface string    `json:"interface,omitempty"` // 实际使用的网卡名（自动识别时为识别结果）
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	LastTime  time.Time `json:"lastTime"`
}

// Worker DDNS 调度器，每个启用的任务一个 goroutine。
type Worker struct {
	cfg *config.Config

	// Notify 事件通知回调（可为 nil），IP 变化更新成功或状态转失败时调用。
	Notify func(event, title, content string)

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	smu    sync.Mutex
	lastIP map[string]string
	status map[string]*TaskStatus
	taskMu map[string]*sync.Mutex // 任务级互斥锁，防手动执行与调度并发
}

func NewWorker(cfg *config.Config) *Worker {
	return &Worker{
		cfg:    cfg,
		lastIP: make(map[string]string),
		status: make(map[string]*TaskStatus),
		taskMu: make(map[string]*sync.Mutex),
	}
}

// lockTask 取任务级互斥锁并加锁。
func (w *Worker) lockTask(taskID string) *sync.Mutex {
	w.smu.Lock()
	m, ok := w.taskMu[taskID]
	if !ok {
		m = &sync.Mutex{}
		w.taskMu[taskID] = m
	}
	w.smu.Unlock()
	m.Lock()
	return m
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

// setStatus 记录最近一次运行状态，返回上一次状态（首次运行返回 nil）。
func (w *Worker) setStatus(taskID, ip, iface string, success bool, msg string) *TaskStatus {
	w.smu.Lock()
	defer w.smu.Unlock()
	var prev *TaskStatus
	if p, ok := w.status[taskID]; ok {
		cp := *p
		prev = &cp
	}
	w.status[taskID] = &TaskStatus{
		TaskID:    taskID,
		IP:        ip,
		Interface: iface,
		Success:   success,
		Message:   msg,
		LastTime:  time.Now(),
	}
	return prev
}

// failWithNotify 记录失败状态，并在“上次成功、本次失败”的状态变化时推送通知。
func (w *Worker) failWithNotify(task config.DDNSTask, ip, iface, errMsg string) {
	prev := w.setStatus(task.ID, ip, iface, false, errMsg)
	if w.Notify != nil && prev != nil && prev.Success {
		w.Notify("ddns", "andey-Proxy DDNS 更新失败",
			fmt.Sprintf("任务: %s\n域名: %s\nIP: %s\n错误: %s",
				task.Name, strings.Join(task.Domains, ", "), ip, errMsg))
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
	m := w.lockTask(task.ID)
	defer m.Unlock()

	ip, iface, err := GetIPDetail(ctx, task)
	if err != nil {
		w.failWithNotify(task, "", "", err.Error())
		return err
	}
	cacheKey := task.ID + "|" + task.IPType
	w.smu.Lock()
	oldIP := w.lastIP[cacheKey]
	changed := oldIP != ip
	if changed {
		w.lastIP[cacheKey] = ip
	}
	w.smu.Unlock()
	if !changed && !force {
		w.setStatus(task.ID, ip, iface, true, "IP 未变化，跳过更新")
		return nil
	}

	recordType := "A"
	if task.IPType == "ipv6" {
		recordType = "AAAA"
	}
	provider, err := w.providerFor(task.ProviderID)
	if err != nil {
		w.failWithNotify(task, ip, iface, err.Error())
		return err
	}
	var msgs []string
	for _, d := range task.Domains {
		msg, err := provider.UpsertRecord(ctx, d, recordType, ip, task.TTL)
		if err != nil {
			w.failWithNotify(task, ip, iface, fmt.Sprintf("%s: %v", d, err))
			return fmt.Errorf("更新 %s 失败: %w", d, err)
		}
		log.Printf("[DDNS] 任务 %s: %s", task.Name, msg)
		msgs = append(msgs, msg)
	}
	result := strings.Join(msgs, "；")
	w.setStatus(task.ID, ip, iface, true, result)
	// 实际执行了更新（IP 变化或强制）才通知成功
	if w.Notify != nil {
		if oldIP == "" {
			oldIP = "（无）"
		}
		w.Notify("ddns", "andey-Proxy DDNS 更新成功",
			fmt.Sprintf("任务: %s\n域名: %s\nIP: %s → %s\n结果: %s",
				task.Name, strings.Join(task.Domains, ", "), oldIP, ip, result))
	}
	return nil
}
