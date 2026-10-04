package tunnel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"andey-proxy/internal/config"
)

func TestSyncRecoversAfterCloudWriteAndLocalSaveFailure(t *testing.T) {
	m, f, _ := newTestManager(t)
	setRoutes(t, m)
	blocked := filepath.Join(m.cfg.Dir(), "config.json.tmp")
	f.afterWrite = func() {
		if err := os.Mkdir(blocked, 0700); err != nil {
			t.Error(err)
		}
	}
	op, err := m.submit("instance", "sync", digestFor(f))
	if err != nil {
		t.Fatal(err)
	}
	if result := waitOperation(t, m, op); result.Status != "failed" {
		t.Fatalf("%+v", result)
	}
	inst, _ := m.instance("instance")
	if inst.Routes[0].AppliedHostname != "" || inst.Routes[0].PendingHostname != "app.example.com" {
		t.Fatal("durable attempt missing or rollback failed")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.afterWrite = nil
	f.mu.Unlock()
	op, err = m.submit("instance", "sync", digestFor(f))
	if err != nil {
		t.Fatal(err)
	}
	if result := waitOperation(t, m, op); result.Status != "succeeded" {
		t.Fatalf("%+v", result)
	}
	inst, _ = m.instance("instance")
	if inst.Routes[0].PendingHostname != "" || !inst.Routes[0].DNSOwned {
		t.Fatal("reconciliation incomplete")
	}
}

func TestRenamePreservesUnselectedPathRouteAndUnknownFields(t *testing.T) {
	remote := cloudConfiguration{Config: map[string]json.RawMessage{
		"ingress": json.RawMessage(`[{"hostname":"old.example.com","path":"/special","service":"http://192.0.2.9","custom":7},{"hostname":"old.example.com","service":"http://192.0.2.1","originRequest":{"connectTimeout":9},"custom":8},{"service":"http_status:404","path":""}]`),
	}}
	inst := config.TunnelInstance{Routes: []config.TunnelRoute{{ID: "r", Hostname: "new.example.com", Target: "url", URL: "https://192.0.2.2", AppliedHostname: "old.example.com", AppliedService: "http://192.0.2.1"}}}
	next, err := mergeIngress(remote, inst, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	json.Unmarshal(next.Config["ingress"], &rows)
	if len(rows) != 3 || string(rows[0]["service"]) != `"http://192.0.2.9"` || string(rows[0]["custom"]) != `7` || string(rows[1]["hostname"]) != `"new.example.com"` || string(rows[1]["custom"]) != `8` {
		t.Fatalf("unselected route changed: %s", next.Config["ingress"])
	}
	var origin map[string]json.RawMessage
	json.Unmarshal(rows[1]["originRequest"], &origin)
	if string(origin["connectTimeout"]) != "9" {
		t.Fatal("unknown origin option lost")
	}
}

func TestBorrowedMatchingDNSIsNeverMarkedOwned(t *testing.T) {
	m, f, _ := newTestManager(t)
	inst, _ := m.instance("instance")
	route := config.TunnelRoute{ID: "r", Hostname: "app.example.com", ZoneID: testZoneID}
	f.records = []dnsRecord{{ID: testRecordID, Name: route.Hostname, Type: "CNAME", Content: testTunnelID + ".cfargotunnel.com", Proxied: true, Comment: "another owner"}}
	c, _ := m.cloud(inst)
	if err := m.ensureDNS(context.Background(), c, inst, &route); err != nil {
		t.Fatal(err)
	}
	if route.DNSOwned || route.DNSRecordID != testRecordID {
		t.Fatal("borrowed record became owned")
	}
}

func TestDeleteReconcilesLostResponse(t *testing.T) {
	m, f, _ := newTestManager(t)
	f.created, f.loseDelete = true, true
	op, err := m.submit("instance", "delete", "")
	if err != nil {
		t.Fatal(err)
	}
	if result := waitOperation(t, m, op); result.Status != "succeeded" {
		t.Fatalf("%+v", result)
	}
	if len(m.instances()) != 0 {
		t.Fatal("deleted instance remains locally")
	}
}
