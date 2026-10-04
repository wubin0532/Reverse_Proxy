package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/webproxy"
)

type Status struct {
	LastOperation *config.TunnelOperation `json:"lastOperation,omitempty"`
	Process       string                  `json:"process"`
	Ready         bool                    `json:"ready"`
	Error         string                  `json:"error,omitempty"`
	Sync          string                  `json:"sync"`
	TargetErrors  []string                `json:"targetErrors"`
}

type Manager struct {
	cfg                  *config.Config
	web                  *webproxy.Service
	ctx                  context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	workers              map[string]*worker
	mutation             sync.Mutex // serializes tunnel mutations and cloud jobs
	reload               sync.Mutex
	wg                   sync.WaitGroup
	clientFactory        func(config.TunnelAccount) *cloudClient
	runtime              func() Runtime
	verifyBinary         func(string, string) error
	freeSpace            func(string) (uint64, error)
	adminPort            int
	releases             *releaseCatalog
	runtimePaused        bool
	runtimeRecoveryError error
}

func NewManager(cfg *config.Config, web *webproxy.Service, adminPort int) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, web: web, ctx: ctx, cancel: cancel, workers: make(map[string]*worker), clientFactory: newCloudClient, runtime: func() Runtime { return detectManagedRuntime(cfg.Dir()) }, verifyBinary: verifyRuntimeBinary, freeSpace: runtimeFreeSpace, releases: newReleaseCatalog(), adminPort: adminPort}
}

func (m *Manager) Start() {
	m.runtimeRecoveryError = recoverRuntime(m.cfg.Dir())
	// Interrupted tasks remain visible and are retried explicitly after reconciliation.
	m.cfg.State().Update(func(s *config.State) {
		for id, op := range s.TunnelOperations {
			if op.Status == "running" {
				op.Status = "interrupted"
				op.Error = "上次操作被中断，请检查云端状态后重试"
				if op.Kind == "runtime-install" {
					op.Error = "上次程序安装被中断，已检查并恢复程序，请重新检查版本后重试"
					if m.runtimeRecoveryError != nil {
						op.Error = "上次程序安装被中断，程序恢复失败，请检查运行目录"
					}
				}
				s.TunnelOperations[id] = op
			}
		}
	})
	_ = m.cfg.State().Flush()
	m.Reload()
}

func (m *Manager) Reload() {
	m.reload.Lock()
	defer m.reload.Unlock()
	if m.ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	paused := m.runtimePaused
	m.mu.Unlock()
	if paused || m.runtimeRecoveryError != nil {
		return
	}
	instances := m.instances()
	want := make(map[string]config.TunnelInstance)
	for _, inst := range instances {
		if inst.Enabled && inst.Token != "" {
			want[inst.ID] = inst
		}
	}
	m.mu.Lock()
	var retired []*worker
	for id, w := range m.workers {
		inst, ok := want[id]
		if !ok || inst.Token != w.instance.Token || inst.Protocol != w.instance.Protocol || inst.IPVersion != w.instance.IPVersion {
			w.cancel()
			retired = append(retired, w)
			delete(m.workers, id)
		}
	}
	m.mu.Unlock()
	for _, w := range retired {
		<-w.done
	}
	if m.web != nil {
		if err := m.web.SyncTunnelOrigins(); err != nil {
			logcenter.Add("tunnel", "", "", "error", "隧道本机入口创建失败")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, inst := range want {
		if m.workers[id] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(m.ctx)
		w := &worker{instance: inst, cancel: cancel, done: make(chan struct{}), status: Status{Process: "starting", Sync: "notSynced", TargetErrors: []string{}}}
		m.workers[id] = w
		m.wg.Add(1)
		go func() { defer m.wg.Done(); defer close(w.done); m.supervise(ctx, w) }()
	}
}

func (m *Manager) Stop() {
	m.reload.Lock()
	m.cancel()
	m.reload.Unlock()
	m.wg.Wait()
}

func (m *Manager) instances() []config.TunnelInstance {
	m.cfg.RLock()
	defer m.cfg.RUnlock()
	// Isolate slices, including routes, from concurrent config mutations.
	data, _ := json.Marshal(m.cfg.Tunnels)
	var result []config.TunnelInstance
	_ = json.Unmarshal(data, &result)
	if result == nil {
		result = []config.TunnelInstance{}
	}
	return result
}

func (m *Manager) instance(id string) (config.TunnelInstance, error) {
	for _, inst := range m.instances() {
		if inst.ID == id {
			return inst, nil
		}
	}
	return config.TunnelInstance{}, errors.New("隧道实例不存在")
}

func (m *Manager) account(id string) (config.TunnelAccount, error) {
	m.cfg.RLock()
	defer m.cfg.RUnlock()
	for _, account := range m.cfg.TunnelAccounts {
		if account.ID == id {
			return account, nil
		}
	}
	return config.TunnelAccount{}, errors.New("账户凭据不存在")
}

func (m *Manager) Status(id string) Status {
	status := Status{Process: "stopped", Sync: "notSynced", TargetErrors: []string{}}
	m.mu.Lock()
	w := m.workers[id]
	m.mu.Unlock()
	if w != nil {
		w.mu.Lock()
		status = w.status
		w.mu.Unlock()
	}
	inst, err := m.instance(id)
	if err != nil {
		return status
	}
	status.TargetErrors = append([]string{}, status.TargetErrors...)
	synced := len(inst.Routes) > 0 && len(inst.RetiredRoutes) == 0
	for _, route := range inst.Routes {
		if route.PendingHostname != "" || route.DNSRecordID == "" || route.AppliedSkipTLSVerify != route.SkipTLSVerify || route.AppliedHostname != route.Hostname || route.AppliedService != routeService(m.cfg.Dir(), route) {
			synced = false
		}
	}
	m.cfg.RLock()
	bindings := m.cfg.TunnelInstanceSiteBindings(inst)
	m.cfg.RUnlock()
	for siteID, hosts := range bindings {
		if m.web == nil {
			continue
		}
		state, _ := m.web.SiteStatus(siteID)
		message := ""
		if state != "listening" {
			message = "站点未运行"
		} else if inst.Enabled {
			originState, originMessage := m.web.TunnelOriginStatus(siteID)
			if originState != "listening" {
				message = originMessage
				if message == "" {
					message = "隧道本机入口未运行"
				}
			}
		}
		if message != "" {
			for host := range hosts {
				status.TargetErrors = append(status.TargetErrors, host+": "+message)
			}
		}
	}

	if synced {
		status.Sync = "synced"
	} else if len(inst.Routes) > 0 || len(inst.RetiredRoutes) > 0 {
		status.Sync = "pending"
	}
	status.LastOperation = m.cfg.State().LatestTunnelOperation(id)
	if status.LastOperation != nil && status.LastOperation.Kind == "sync" && status.LastOperation.Status == "failed" {
		status.Sync = "failed"
	}
	return status
}

func (m *Manager) LockForRestore() (func(), error) {
	if !m.mutation.TryLock() {
		return nil, errors.New("隧道操作正在进行，请完成后再导入备份")
	}
	return m.mutation.Unlock, nil
}
