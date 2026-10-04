package tunnel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"andey-proxy/internal/logcenter"
)

func (m *Manager) runtimeView() Runtime {
	rt := m.runtime()
	rt.Operation = m.cfg.State().LatestTunnelOperation("")
	if m.runtimeRecoveryError != nil {
		rt.Compatible = false
		rt.Message = "程序安装恢复失败，请检查运行目录"
	}
	return rt
}

func (m *Manager) installRuntime(releaseID string, restart bool, expected []string) (config.TunnelOperation, error) {
	if !m.mutation.TryLock() {
		return config.TunnelOperation{}, fmt.Errorf("已有隧道操作正在进行")
	}
	m.reload.Lock()
	defer m.reload.Unlock()
	release, err := m.releases.selected(releaseID)
	if err != nil {
		m.mutation.Unlock()
		return config.TunnelOperation{}, err
	}
	if runtimeAsset(m.runtime().GOOS, m.runtime().GOARCH) == "" {
		m.mutation.Unlock()
		return config.TunnelOperation{}, fmt.Errorf("此架构不支持自动安装")
	}
	if m.ctx.Err() != nil {
		m.mutation.Unlock()
		return config.TunnelOperation{}, fmt.Errorf("服务正在停止")
	}
	if m.runtimeRecoveryError != nil {
		m.mutation.Unlock()
		return config.TunnelOperation{}, fmt.Errorf("上次程序安装尚未恢复，请检查运行目录后重启服务")
	}
	affected := []string{}
	for _, inst := range m.instances() {
		if inst.Enabled {
			affected = append(affected, inst.ID)
		}
		if inst.Enabled && !restart {
			m.mutation.Unlock()
			return config.TunnelOperation{}, fmt.Errorf("请确认重启所有启用的隧道实例")
		}
	}
	slices.Sort(affected)
	expected = slices.Clone(expected)
	slices.Sort(expected)
	if !slices.Equal(affected, expected) {
		m.mutation.Unlock()
		return config.TunnelOperation{}, fmt.Errorf("受影响实例发生变化，请刷新后重新确认")
	}
	op := config.TunnelOperation{ID: ids.New(), Kind: "runtime-install", Status: "running", Stage: "preflight", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Version: release.Version, TotalBytes: release.Size}
	if err = m.recordOperation(op); err != nil {
		m.mutation.Unlock()
		return op, fmt.Errorf("无法保存下载任务")
	}
	m.wg.Add(1)
	go func(op config.TunnelOperation) {
		defer m.wg.Done()
		defer m.mutation.Unlock()
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
		defer cancel()
		err := m.performRuntimeInstall(ctx, &op, release)
		if err != nil {
			op.Status = "failed"
			op.Error = err.Error()
		} else {
			op.Status = "succeeded"
			op.Stage = "complete"
		}
		if err = m.recordOperation(op); err != nil {
			logcenter.Add("tunnel", "", "", "error", "无法保存程序安装任务结果")
		}
	}(op)
	return op, nil
}

func (m *Manager) pauseRuntime() map[string]bool {
	m.reload.Lock()
	defer m.reload.Unlock()
	m.mu.Lock()
	m.runtimePaused = true
	workers := []*worker{}
	ready := map[string]bool{}
	for id, w := range m.workers {
		w.mu.Lock()
		ready[id] = w.status.Ready
		w.mu.Unlock()
		w.cancel()
		workers = append(workers, w)
		delete(m.workers, id)
	}
	m.mu.Unlock()
	for _, w := range workers {
		<-w.done
	}
	return ready
}

func (m *Manager) resumeRuntime() {
	m.reload.Lock()
	m.mu.Lock()
	m.runtimePaused = false
	m.mu.Unlock()
	m.reload.Unlock()
	m.Reload()
}

func (m *Manager) confirmRuntime(ctx context.Context, prior map[string]bool) error {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	stableSince := time.Time{}
	for {
		complete := true
		for _, inst := range m.instances() {
			if !inst.Enabled {
				continue
			}
			status := m.Status(inst.ID)
			if status.Process == "retrying" {
				return fmt.Errorf("新程序启动失败")
			}
			if status.Process != "running" || prior[inst.ID] && !status.Ready {
				complete = false
			}
		}
		if complete && stableSince.IsZero() {
			stableSince = time.Now()
		}
		if !complete {
			stableSince = time.Time{}
		}
		if complete && time.Since(stableSince) >= 2*time.Second {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("程序更新被中断")
		case <-deadline.C:
			return fmt.Errorf("新程序未恢复更新前的连接状态")
		case <-ticker.C:
		}
	}
}

func (m *Manager) performRuntimeInstall(ctx context.Context, op *config.TunnelOperation, release Release) (result error) {
	dir := m.cfg.Dir()
	if err := prepareRuntimeDir(dir); err != nil {
		return err
	}
	free, err := m.freeSpace(runtimeDir(dir))
	if err != nil || free < uint64(release.Size+(8<<20)) {
		return fmt.Errorf("磁盘空间不足，至少需要下载大小及 8 MiB 余量")
	}
	if err = m.stage(op, "downloadRuntime"); err != nil {
		return err
	}
	path := filepath.Join(runtimeDir(dir), ".download-"+op.ID)
	defer os.Remove(path)
	last := time.Now()
	err = m.releases.download(ctx, release, path, func(n int64) {
		op.DownloadedBytes = n
		if time.Since(last) > time.Second {
			_ = m.recordOperation(*op)
			last = time.Now()
		}
	})
	if err != nil {
		return err
	}
	if err = m.stage(op, "verifyRuntime"); err != nil {
		return err
	}
	if err = m.verifyBinary(path, release.Version); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return fmt.Errorf("程序下载任务被中断")
	}
	target := managedRuntimePath(dir)
	info, err := os.Lstat(target)
	hadManaged := err == nil
	if hadManaged && !info.Mode().IsRegular() {
		return fmt.Errorf("当前受管理程序路径不安全")
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("无法检查当前程序")
	}
	if err = m.stage(op, "switchRuntime"); err != nil {
		return err
	}
	if err = saveRuntimeJournal(dir, runtimeJournal{HadManaged: hadManaged}); err != nil {
		return fmt.Errorf("无法保存程序安装恢复记录")
	}
	prior := m.pauseRuntime()
	switched := false
	defer func() {
		if result != nil {
			if switched {
				_ = m.pauseRuntime()
			}
			if rollback := recoverRuntime(dir); rollback != nil {
				result = fmt.Errorf("%w；恢复旧程序失败: %v", result, rollback)
			}
			m.resumeRuntime()
		}
	}()
	if hadManaged {
		if err = os.Rename(target, target+".previous"); err != nil {
			return fmt.Errorf("无法备份当前程序")
		}
	}
	if err = os.Rename(path, target); err != nil {
		return fmt.Errorf("无法安装下载程序")
	}
	switched = true
	if err = syncRuntimeDir(dir); err != nil {
		return fmt.Errorf("无法保存新程序")
	}
	if err = m.stage(op, "restartRuntime"); err != nil {
		return err
	}
	m.resumeRuntime()
	if err = m.confirmRuntime(ctx, prior); err != nil {
		return err
	}
	op.Stage = "commitRuntime"
	if err = m.recordOperation(*op); err != nil {
		return fmt.Errorf("无法保存程序安装结果")
	}
	if err = saveRuntimeJournal(dir, runtimeJournal{HadManaged: hadManaged, Committed: true}); err != nil {
		return fmt.Errorf("无法确认程序安装结果")
	}
	op.Status = "succeeded"
	op.Stage = "complete"
	if err = m.recordOperation(*op); err != nil {
		// Restore on an unsuccessful durable operation commit.
		if journalErr := saveRuntimeJournal(dir, runtimeJournal{HadManaged: hadManaged}); journalErr != nil {
			return fmt.Errorf("新程序已安装，但无法保存任务结果及恢复记录")
		}
		return fmt.Errorf("无法保存程序安装结果")
	}
	if err = recoverRuntime(dir); err != nil {
		return fmt.Errorf("新程序已安装，但清理安装记录失败")
	}
	return nil
}
