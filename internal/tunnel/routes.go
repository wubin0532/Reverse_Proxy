package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"andey-proxy/internal/config"
)

func configDigest(remote cloudConfiguration) string {
	data, _ := json.Marshal(remote.Config)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Current applied/pending metadata takes precedence over retired history after
// a partially completed sync; old hostnames remain available for cleanup.
func ownedRouteBindings(inst config.TunnelInstance) map[string]config.TunnelRoute {
	owned := make(map[string]config.TunnelRoute)
	for _, route := range append(append([]config.TunnelRoute{}, inst.RetiredRoutes...), inst.Routes...) {
		if route.AppliedHostname != "" {
			owned[route.AppliedHostname] = route
		}
		if route.PendingHostname != "" {
			owned[route.PendingHostname] = route
		}
	}
	return owned
}

// Merge only selected exact-hostname entries; preserve all other raw fields/order.
func mergeIngress(remote cloudConfiguration, inst config.TunnelInstance, dir string) (cloudConfiguration, error) {
	var ingress []map[string]json.RawMessage
	if raw := remote.Config["ingress"]; len(raw) > 0 && json.Unmarshal(raw, &ingress) != nil {
		return remote, fmt.Errorf("云端路由配置格式无效")
	}
	owned := ownedRouteBindings(inst)
	desired := make(map[string]config.TunnelRoute)
	byID := make(map[string]config.TunnelRoute)
	for _, route := range inst.Routes {
		desired[route.Hostname] = route
		byID[route.ID] = route
	}
	seen := make(map[string]bool)
	out := []map[string]json.RawMessage{}
	newEntries := func() {
		for _, route := range inst.Routes {
			if !seen[route.Hostname] {
				out = append(out, updatedEntry(nil, route, dir))
				seen[route.Hostname] = true
			}
		}
	}
	catchAll := false
	for _, entry := range ingress {
		var hostname, service, path string
		_ = json.Unmarshal(entry["hostname"], &hostname)
		_ = json.Unmarshal(entry["service"], &service)
		_ = json.Unmarshal(entry["path"], &path)
		previous, isOwned := owned[hostname]
		// Exact-host routes never take ownership of existing path-specific rules.
		isOwned = isOwned && path == ""
		if isOwned {
			if previous.AppliedService != service && previous.PendingService != service {
				return remote, fmt.Errorf("已管理路由的云端服务发生变化，请刷新后检查")
			}
			if route, ok := byID[previous.ID]; ok {
				if seen[route.Hostname] {
					return remote, fmt.Errorf("云端有重复的完整域名路由，请先检查")
				}
				out = append(out, updatedEntry(entry, route, dir))
				seen[route.Hostname] = true
			}
			continue
		}
		if _, conflict := desired[hostname]; conflict && path == "" {
			return remote, fmt.Errorf("云端已有同名路由，请先选择导入该条路由")
		}
		if hostname == "" && path == "" {
			newEntries()
			catchAll = true
		}
		out = append(out, entry)
	}
	if !catchAll {
		newEntries()
		out = append(out, map[string]json.RawMessage{"service": json.RawMessage(`"http_status:404"`)})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return remote, err
	}
	remote.Config["ingress"] = data
	return remote, nil
}

func updatedEntry(original map[string]json.RawMessage, route config.TunnelRoute, dir string) map[string]json.RawMessage {
	entry := make(map[string]json.RawMessage)
	for key, value := range original {
		entry[key] = value
	}
	entry["hostname"], _ = json.Marshal(route.Hostname)
	entry["service"], _ = json.Marshal(routeService(dir, route))
	origin := make(map[string]json.RawMessage)
	_ = json.Unmarshal(entry["originRequest"], &origin)
	if origin == nil {
		origin = make(map[string]json.RawMessage)
	}
	origin["noTLSVerify"], _ = json.Marshal(route.SkipTLSVerify)
	if route.Target == "site" {
		origin["httpHostHeader"], _ = json.Marshal(route.Hostname)
	} else {
		var oldHost string
		_ = json.Unmarshal(origin["httpHostHeader"], &oldHost)
		if strings.HasPrefix(route.AppliedService, "unix:") && oldHost == route.AppliedHostname {
			delete(origin, "httpHostHeader")
		}
	}
	entry["originRequest"], _ = json.Marshal(origin)
	return entry
}

func (m *Manager) syncCloud(ctx context.Context, op *config.TunnelOperation, digest string) error {
	inst, err := m.instance(op.InstanceID)
	if err != nil {
		return err
	}
	if inst.TunnelID == "" {
		return fmt.Errorf("请先完成云端隧道创建")
	}
	c, err := m.cloud(inst)
	if err != nil {
		return err
	}
	if err = m.stage(op, "preflight"); err != nil {
		return err
	}
	if err = m.checkConnectors(ctx, c, inst); err != nil {
		return err
	}
	remote, err := c.configuration(ctx, inst.TunnelID)
	if err != nil {
		return err
	}
	if digest == "" || configDigest(remote) != digest {
		return fmt.Errorf("云端配置已变化或尚未读取，请刷新路由后重新同步")
	}
	for i := range inst.Routes {
		if err = m.validateRoute(&inst.Routes[i]); err != nil {
			return err
		}
	}
	zones, err := c.zones(ctx)
	if err != nil {
		return err
	}
	for _, route := range inst.Routes {
		valid := false
		for _, zone := range zones {
			if zone.ID == route.ZoneID && (route.Hostname == zone.Name || hasDomainSuffix(route.Hostname, zone.Name)) {
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("路由域名不属于所选账户和域名区域")
		}
		if _, err = m.preflightDNS(ctx, c, inst, route); err != nil {
			return err
		}
	}
	next, err := mergeIngress(remote, inst, m.cfg.Dir())
	if err != nil {
		return err
	}
	// Recheck immediately before PUT; Cloudflare does not offer an atomic config CAS.
	current, err := c.configuration(ctx, inst.TunnelID)
	if err != nil {
		return err
	}
	if configDigest(current) != digest {
		return fmt.Errorf("云端配置在检查期间发生变化，请刷新后重新同步")
	}
	if err = m.stage(op, "writeRoutes"); err != nil {
		return err
	}
	// Journal the exact attempted entries before PUT. A crash or failed local save
	// after Cloudflare accepts the update must not turn them into foreign routes.
	for i := range inst.Routes {
		inst.Routes[i].PendingHostname = inst.Routes[i].Hostname
		inst.Routes[i].PendingService = routeService(m.cfg.Dir(), inst.Routes[i])
	}
	if err = m.saveInstance(inst); err != nil {
		return fmt.Errorf("无法保存路由提交记录，已停止云端写入")
	}
	if err = c.do(ctx, "PUT", c.base()+"/"+inst.TunnelID+"/configurations", next, nil); err != nil {
		observed, queryErr := c.configuration(ctx, inst.TunnelID)
		if queryErr != nil || configDigest(observed) != configDigest(next) {
			return err
		}
	}
	for i := range inst.Routes {
		r := &inst.Routes[i]
		r.AppliedHostname = r.Hostname
		r.AppliedService = routeService(m.cfg.Dir(), *r)
		r.AppliedSkipTLSVerify = r.SkipTLSVerify
		r.PendingHostname, r.PendingService = "", ""
	}
	if err = m.saveInstance(inst); err != nil {
		return fmt.Errorf("云端路由已更新，但本地保存失败，请刷新云端状态")
	}
	if err = m.stage(op, "writeDNS"); err != nil {
		return err
	}
	for i := range inst.Routes {
		if err = m.ensureDNS(ctx, c, inst, &inst.Routes[i]); err != nil {
			return err
		}
		if err = m.saveInstance(inst); err != nil {
			return fmt.Errorf("DNS 已创建，但本地记录保存失败，请检查 DNS 后重试")
		}
	}
	if err = m.stage(op, "cleanupDNS"); err != nil {
		return err
	}
	for _, route := range inst.RetiredRoutes {
		if retiredDNSInUse(inst, next, route) {
			continue
		}
		if err = m.cleanupDNS(ctx, c, inst, route); err != nil {
			return err
		}
	}
	inst.RetiredRoutes = nil
	return m.saveInstance(inst)
}
