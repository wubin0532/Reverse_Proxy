package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/notify"
)

func (m *Manager) recordOperation(op config.TunnelOperation) error {
	m.cfg.State().Update(func(s *config.State) {
		if s.TunnelOperations == nil {
			s.TunnelOperations = make(map[string]config.TunnelOperation)
		}
		s.TunnelOperations[op.ID] = op
		if len(s.TunnelOperations) > 50 {
			all := []config.TunnelOperation{}
			for _, item := range s.TunnelOperations {
				if item.Status != "running" {
					all = append(all, item)
				}
			}
			sort.Slice(all, func(i, j int) bool { return all[i].StartedAt < all[j].StartedAt })
			for _, item := range all {
				if len(s.TunnelOperations) <= 50 {
					break
				}
				delete(s.TunnelOperations, item.ID)
			}
		}
	})
	return m.cfg.State().Flush()
}

func (m *Manager) submit(id, kind, digest string) (config.TunnelOperation, error) {
	if !m.mutation.TryLock() {
		return config.TunnelOperation{}, fmt.Errorf("已有隧道操作正在进行")
	}
	m.reload.Lock()
	defer m.reload.Unlock()
	if m.ctx.Err() != nil {
		m.mutation.Unlock()
		return config.TunnelOperation{}, fmt.Errorf("服务正在停止")
	}
	if _, err := m.instance(id); err != nil {
		m.mutation.Unlock()
		return config.TunnelOperation{}, err
	}
	op := config.TunnelOperation{ID: ids.New(), InstanceID: id, Kind: kind, Status: "running", Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := m.recordOperation(op); err != nil {
		m.mutation.Unlock()
		return op, fmt.Errorf("无法保存操作进度")
	}
	m.wg.Add(1)
	go func(op config.TunnelOperation) {
		defer m.wg.Done()
		defer m.mutation.Unlock()
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
		defer cancel()
		var err error
		switch kind {
		case "create":
			err = m.createCloud(ctx, &op)
		case "sync":
			err = m.syncCloud(ctx, &op, digest)
		case "delete":
			err = m.deleteCloud(ctx, &op)
		default:
			err = fmt.Errorf("未知操作")
		}
		if err != nil {
			op.Status = "failed"
			op.Error = err.Error()
			notify.Publish(notify.Event{Type: "tunnel.operation_failed", Level: "error", Entity: id, Message: op.Error})
		} else {
			op.Status = "succeeded"
			op.Stage = "complete"
		}
		if saveErr := m.recordOperation(op); saveErr != nil {
			logcenter.Add("tunnel", id, "", "error", "保存隧道操作结果失败")
		}
		level := "info"
		if err != nil {
			level = "error"
		}
		logcenter.Add("tunnel", id, "", level, "隧道操作 "+kind+": "+op.Status+" "+op.Error)
		m.Reload()
	}(op)
	return op, nil
}

func (m *Manager) stage(op *config.TunnelOperation, name string) error {
	op.Stage = name
	if err := m.recordOperation(*op); err != nil {
		return fmt.Errorf("无法保存操作进度，已停止后续云端操作")
	}
	return nil
}

func (m *Manager) saveInstance(inst config.TunnelInstance) error {
	// Jobs continue editing their snapshot after each commit. Never share those
	// slices with Config: it would bypass its lock and persistence rollback.
	inst.Routes = append([]config.TunnelRoute(nil), inst.Routes...)
	inst.RetiredRoutes = append([]config.TunnelRoute(nil), inst.RetiredRoutes...)
	return m.cfg.Update(func(c *config.Config) error {
		for i := range c.Tunnels {
			if c.Tunnels[i].ID == inst.ID {
				c.Tunnels[i] = inst
				return nil
			}
		}
		return fmt.Errorf("隧道实例不存在")
	})
}

func (m *Manager) createCloud(ctx context.Context, op *config.TunnelOperation) error {
	inst, err := m.instance(op.InstanceID)
	if err != nil {
		return err
	}
	if inst.Source != "managed" {
		return fmt.Errorf("导入实例不能创建云端隧道")
	}
	c, err := m.cloud(inst)
	if err != nil {
		return err
	}
	if err = m.stage(op, "createTunnel"); err != nil {
		return err
	}
	// The reserved name makes a timed-out create discoverable before another POST.
	name := "andey-" + inst.ID
	if inst.TunnelID == "" {
		find := func() ([]cloudTunnel, error) {
			var found []cloudTunnel
			err := c.do(ctx, "GET", c.base()+"?is_deleted=false&name="+name, nil, &found)
			return found, err
		}
		found, err := find()
		if err != nil {
			return err
		}
		if len(found) > 1 {
			return fmt.Errorf("发现重复的云端隧道，请先在 Cloudflare 检查")
		}
		var remote cloudTunnel
		if len(found) == 1 {
			remote = found[0]
		} else {
			err = c.do(ctx, "POST", c.base(), map[string]string{"name": name, "config_src": "cloudflare"}, &remote)
			if err != nil {
				found, queryErr := find()
				if queryErr != nil || len(found) != 1 {
					return err
				}
				remote = found[0]
			}
		}
		if !uuidPattern.MatchString(remote.ID) {
			return fmt.Errorf("Cloudflare 返回了无效的 Tunnel ID")
		}
		inst.TunnelID = remote.ID
		op.RemoteID = remote.ID
		if err = m.stage(op, "saveTunnelID"); err != nil {
			return err
		}
		if err = m.saveInstance(inst); err != nil {
			return fmt.Errorf("隧道已创建但本地保存失败，请重试完成创建")
		}
	}
	if inst.Token == "" {
		inst.Token, err = c.token(ctx, inst.TunnelID)
		if err != nil {
			return err
		}
		if err = m.validateCredentials(&inst); err != nil {
			return err
		}
		if err = m.saveInstance(inst); err != nil {
			return fmt.Errorf("保存 Tunnel Token 失败")
		}
	}
	// New instances start with no public routes and a catch-all 404.
	if err = m.stage(op, "initializeRoutes"); err != nil {
		return err
	}
	remote, err := c.configuration(ctx, inst.TunnelID)
	if err != nil {
		return err
	}
	var ingress []json.RawMessage
	if json.Unmarshal(remote.Config["ingress"], &ingress) != nil || len(ingress) == 0 {
		remote.Config["ingress"] = json.RawMessage(`[{"service":"http_status:404"}]`)
		return c.do(ctx, "PUT", c.base()+"/"+inst.TunnelID+"/configurations", remote, nil)
	}
	return nil
}
