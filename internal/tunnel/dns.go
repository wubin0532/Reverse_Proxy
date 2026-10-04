package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

func dnsMarker(inst config.TunnelInstance, route config.TunnelRoute) string {
	return "andey-proxy:" + inst.ID + ":" + route.ID
}

func hasDomainSuffix(host, zone string) bool { return strings.HasSuffix(host, "."+zone) }

func (m *Manager) preflightDNS(ctx context.Context, c *cloudClient, inst config.TunnelInstance, route config.TunnelRoute) ([]dnsRecord, error) {
	records, err := c.records(ctx, route.ZoneID, route.Hostname)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.Type != "CNAME" || !strings.EqualFold(strings.TrimSuffix(record.Content, "."), inst.TunnelID+".cfargotunnel.com") || !record.Proxied {
			return nil, fmt.Errorf("域名 %s 存在 DNS 冲突，不会覆盖现有记录", route.Hostname)
		}
	}
	return records, nil
}

func (m *Manager) ensureDNS(ctx context.Context, c *cloudClient, inst config.TunnelInstance, route *config.TunnelRoute) error {
	records, err := m.preflightDNS(ctx, c, inst, *route)
	if err != nil {
		return err
	}
	if len(records) > 0 {
		route.DNSOwned = records[0].Comment == dnsMarker(inst, *route)
		route.DNSRecordID = records[0].ID
		return nil
	}
	// Persist ownership intent before POST so a lost response can be reconciled.
	route.DNSOwned = false
	route.DNSRecordID = ""
	for i := range inst.Routes {
		if inst.Routes[i].ID == route.ID {
			inst.Routes[i] = *route
		}
	}
	if err = m.saveInstance(inst); err != nil {
		return fmt.Errorf("无法保存 DNS 创建进度")
	}
	var record dnsRecord
	err = c.do(ctx, "POST", "/zones/"+route.ZoneID+"/dns_records", map[string]any{"type": "CNAME", "name": route.Hostname, "content": inst.TunnelID + ".cfargotunnel.com", "proxied": true, "ttl": 1, "comment": dnsMarker(inst, *route)}, &record)
	if err != nil {
		found, queryErr := m.preflightDNS(ctx, c, inst, *route)
		if queryErr != nil || len(found) != 1 {
			return err
		}
		record = found[0]
	}
	if !cloudIDPattern.MatchString(record.ID) {
		return fmt.Errorf("Cloudflare 返回了无效 DNS 记录 ID，请检查后重试")
	}
	route.DNSRecordID = record.ID
	route.DNSOwned = record.Comment == dnsMarker(inst, *route)
	return nil
}

func (m *Manager) cleanupDNS(ctx context.Context, c *cloudClient, inst config.TunnelInstance, route config.TunnelRoute) error {
	if !route.DNSOwned || route.DNSRecordID == "" {
		return nil
	}
	host := route.AppliedHostname
	if host == "" {
		host = route.Hostname
	}
	records, err := c.records(ctx, route.ZoneID, host)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.ID != route.DNSRecordID {
			continue
		}
		if record.Comment != dnsMarker(inst, route) || record.Type != "CNAME" || !strings.EqualFold(strings.TrimSuffix(record.Content, "."), inst.TunnelID+".cfargotunnel.com") {
			logcenter.Add("tunnel", inst.ID, "", "warn", "DNS 已被外部修改，保留记录: "+host)
			return nil
		}
		if err = c.do(ctx, "DELETE", "/zones/"+route.ZoneID+"/dns_records/"+record.ID, nil, nil); err != nil {
			observed, queryErr := c.records(ctx, route.ZoneID, host)
			if queryErr != nil {
				return err
			}
			for _, item := range observed {
				if item.ID == record.ID {
					return err
				}
			}
		}
	}
	return nil
}

// Retired DNS may be reused by a local route or an unselected cloud path rule.
func retiredDNSInUse(inst config.TunnelInstance, remote cloudConfiguration, retired config.TunnelRoute) bool {
	host := retired.AppliedHostname
	if host == "" {
		host = retired.Hostname
	}
	for _, active := range inst.Routes {
		if active.ZoneID == retired.ZoneID && (active.Hostname == host || (retired.DNSRecordID != "" && active.DNSRecordID == retired.DNSRecordID)) {
			return true
		}
	}
	var ingress []struct {
		Hostname string `json:"hostname"`
	}
	_ = json.Unmarshal(remote.Config["ingress"], &ingress)
	for _, entry := range ingress {
		if strings.EqualFold(entry.Hostname, host) {
			return true
		}
	}
	return false
}

func (m *Manager) deleteCloud(ctx context.Context, op *config.TunnelOperation) error {
	inst, err := m.instance(op.InstanceID)
	if err != nil {
		return err
	}
	if inst.Source != "managed" || inst.TunnelID == "" {
		return fmt.Errorf("仅能删除本项目创建的云端隧道")
	}
	c, err := m.cloud(inst)
	if err != nil {
		return err
	}
	inst.Enabled = false
	if err = m.saveInstance(inst); err != nil {
		return err
	}
	m.Reload()
	if err = m.stage(op, "cleanupDNS"); err != nil {
		return err
	}
	for _, route := range append(append([]config.TunnelRoute{}, inst.Routes...), inst.RetiredRoutes...) {
		if err = m.cleanupDNS(ctx, c, inst, route); err != nil {
			return err
		}
	}
	if err = m.stage(op, "deleteTunnel"); err != nil {
		return err
	}
	if err = c.do(ctx, "DELETE", c.base()+"/"+inst.TunnelID, nil, nil); err != nil {
		var remaining []cloudTunnel
		queryErr := c.do(ctx, "GET", c.base()+"?is_deleted=false&uuid="+inst.TunnelID, nil, &remaining)
		if queryErr != nil || len(remaining) != 0 {
			return err
		}
	}
	return m.removeInstance(inst.ID)
}

func (m *Manager) removeInstance(id string) error {
	return m.cfg.Update(func(c *config.Config) error {
		for i := range c.Tunnels {
			if c.Tunnels[i].ID == id {
				c.Tunnels = append(c.Tunnels[:i], c.Tunnels[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("隧道实例不存在")
	})
}
