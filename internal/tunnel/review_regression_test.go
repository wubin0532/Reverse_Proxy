package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"andey-proxy/internal/config"
	"andey-proxy/internal/webproxy"
)

func publishedTestRoute() config.TunnelRoute {
	return config.TunnelRoute{ID: "route", Hostname: "keep.example.com", ZoneID: testZoneID, Target: "url", URL: "http://192.0.2.1:80", AppliedHostname: "keep.example.com", AppliedService: "http://192.0.2.1:80", DNSOwned: true, DNSRecordID: testRecordID}
}

func TestRenameRevertPreservesActiveDNS(t *testing.T) {
	m, f, h := newTestManager(t)
	inst, _ := m.instance("instance")
	inst.Routes = []config.TunnelRoute{publishedTestRoute()}
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	f.records = []dnsRecord{{ID: testRecordID, Name: "keep.example.com", Type: "CNAME", Content: testTunnelID + ".cfargotunnel.com", Proxied: true, Comment: dnsMarker(inst, inst.Routes[0])}}
	for _, host := range []string{"other.example.com", "keep.example.com"} {
		body := fmt.Sprintf(`{"routes":[{"id":"route","hostname":%q,"zoneId":%q,"target":"url","url":"http://192.0.2.1:80"}]}`, host, testZoneID)
		resp := request(t, h, "PUT", "/api/tunnels/instances/instance/routes", body)
		if resp.Code != 200 {
			t.Fatal(resp.Body.String())
		}
	}
	op, err := m.submit("instance", "sync", digestFor(f))
	if err != nil {
		t.Fatal(err)
	}
	if done := waitOperation(t, m, op); done.Status != "succeeded" {
		t.Fatalf("%+v", done)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) != 1 || f.dnsDeletes != 0 {
		t.Fatal("active DNS deleted")
	}
	inst, _ = m.instance("instance")
	if len(inst.RetiredRoutes) != 0 || inst.Routes[0].DNSRecordID != testRecordID || !inst.Routes[0].DNSOwned {
		t.Fatalf("invalid final metadata: %+v", inst)
	}
}

func TestRetiredDNSStillUsedByUnselectedPathRule(t *testing.T) {
	m, f, _ := newTestManager(t)
	inst, _ := m.instance("instance")
	route := publishedTestRoute()
	inst.RetiredRoutes = []config.TunnelRoute{route}
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	f.remote.Config["ingress"] = json.RawMessage(`[{"hostname":"keep.example.com","service":"http://192.0.2.1:80"},{"hostname":"keep.example.com","path":"/other","service":"http://192.0.2.2:80"},{"service":"http_status:404"}]`)
	f.records = []dnsRecord{{ID: testRecordID, Name: route.Hostname, Type: "CNAME", Content: testTunnelID + ".cfargotunnel.com", Proxied: true, Comment: dnsMarker(inst, route)}}
	op := config.TunnelOperation{ID: "path-preserve", InstanceID: inst.ID}
	if err := m.syncCloud(context.Background(), &op, digestFor(f)); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 1 || f.dnsDeletes != 0 || !strings.Contains(string(f.remote.Config["ingress"]), "/other") {
		t.Fatal("unselected path route lost DNS")
	}
}

func TestRetiredRouteRequiresPendingStatus(t *testing.T) {
	m, _, _ := newTestManager(t)
	inst, _ := m.instance("instance")
	inst.Routes = []config.TunnelRoute{publishedTestRoute()}
	inst.RetiredRoutes = []config.TunnelRoute{{ID: "removed", AppliedHostname: "old.example.com", AppliedService: "http://192.0.2.2:80"}}
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	if status := m.Status(inst.ID); status.Sync != "pending" {
		t.Fatalf("status=%s", status.Sync)
	}
	inst.RetiredRoutes = nil
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	if status := m.Status(inst.ID); status.Sync != "synced" {
		t.Fatalf("status=%s", status.Sync)
	}
}

func TestRetiredHistoryDoesNotBlockRecoveryAfterRoutesWrittenButDNSFailed(t *testing.T) {
	m, f, _ := newTestManager(t)
	inst, _ := m.instance("instance")
	old := publishedTestRoute()
	active := old
	active.URL = "http://192.0.2.99:80"
	inst.Routes, inst.RetiredRoutes = []config.TunnelRoute{active}, []config.TunnelRoute{old}
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	f.afterWrite = func() {
		f.records = []dnsRecord{{ID: testRecordID, Name: old.Hostname, Type: "A", Content: "192.0.2.10"}}
	}
	op := config.TunnelOperation{ID: "partial-dns", InstanceID: inst.ID}
	if err := m.syncCloud(context.Background(), &op, digestFor(f)); err == nil || !strings.Contains(err.Error(), "DNS 冲突") {
		t.Fatalf("expected DNS failure after PUT: %v", err)
	}
	inst, _ = m.instance(inst.ID)
	if inst.Routes[0].AppliedService != active.URL || len(inst.RetiredRoutes) == 0 {
		t.Fatal("partial progress not persisted")
	}
	f.afterWrite = nil
	f.records = []dnsRecord{{ID: testRecordID, Name: old.Hostname, Type: "CNAME", Content: testTunnelID + ".cfargotunnel.com", Proxied: true, Comment: dnsMarker(inst, old)}}
	if err := m.syncCloud(context.Background(), &op, digestFor(f)); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 1 || f.dnsDeletes != 0 {
		t.Fatal("recovered route lost DNS")
	}
}

func TestSocketFailureVisibleAndRecoveryClearsTargetError(t *testing.T) {
	m, _, _ := newTestManager(t)
	if err := m.cfg.Update(func(c *config.Config) error {
		c.Sites = []config.Site{{ID: "site", Name: "review", Listen: "127.0.0.1:0", Enabled: true}, {ID: "other", Name: "other", Listen: "127.0.0.1:0", Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inst, _ := m.instance("instance")
	route := publishedTestRoute()
	route.Target, route.SiteID, route.URL = "site", "site", ""
	route.AppliedService = routeService(m.cfg.Dir(), route)
	other := route
	other.ID, other.SiteID, other.Hostname, other.AppliedHostname = "other", "other", "other.example.com", "other.example.com"
	other.AppliedService = routeService(m.cfg.Dir(), other)
	inst.Routes, inst.Enabled = []config.TunnelRoute{route, other}, true
	if err := m.saveInstance(inst); err != nil {
		t.Fatal(err)
	}
	svc := webproxy.NewService(m.cfg, nil)
	svc.Start()
	t.Cleanup(svc.Stop)
	m.web = svc
	if state, _ := svc.SiteStatus("site"); state != "listening" {
		t.Fatal("TCP site unavailable")
	}
	path := webproxy.TunnelSocketPath(m.cfg.Dir(), "site")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path); os.Remove(filepath.Dir(path)) })
	if err := os.WriteFile(path, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncTunnelOrigins(); err == nil {
		t.Fatal("expected socket creation failure")
	}
	if state, _ := svc.TunnelOriginStatus("other"); state != "listening" {
		t.Fatal("one failed origin blocked an unrelated site")
	}
	m.workers["instance"] = &worker{status: Status{Process: "running", Ready: true, TargetErrors: []string{}}}
	for i := 0; i < 2; i++ {
		status := m.Status("instance")
		if !status.Ready || len(status.TargetErrors) != 1 || !strings.Contains(status.TargetErrors[0], "keep.example.com") {
			t.Fatalf("%+v", status)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncTunnelOrigins(); err != nil {
		t.Fatal(err)
	}
	if status := m.Status("instance"); len(status.TargetErrors) != 0 {
		t.Fatalf("recovery retained errors: %+v", status)
	}
}
