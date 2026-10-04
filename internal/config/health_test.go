package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func healthConfig(t *testing.T, dir string) *Config {
	t.Helper()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.State().Close() })
	return c
}

func testHealthConf() HealthCheckConf {
	return HealthCheckConf{Enabled: true, Type: "http", Path: "/healthy", IntervalSeconds: 17, TimeoutSeconds: 3, Rise: 2, Fall: 3}
}

func TestLegacyHealthChecksMigrateOnceAndSurviveBackupRestore(t *testing.T) {
	dir := t.TempDir()
	want := testHealthConf()
	plain := []byte(`{"settings":{"adminUser":"admin","adminPassHash":"hash"},"sites":[{"id":"site","rules":[{"id":"rule","type":"reverse"}]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), plain, 0600); err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(map[string]any{"rules": map[string]HealthCheckConf{"rule": want}})
	if err := os.WriteFile(filepath.Join(dir, "webproxy-health.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	c := healthConfig(t, dir)
	if c.RuleHealthChecks["rule"] != want {
		t.Fatal("legacy configuration lost")
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || bytes.Contains(data, []byte("/healthy")) {
		t.Fatal("health checks not encrypted")
	}
	// An old sidecar cannot overwrite new settings or resurrect them after restore.
	if err := os.WriteFile(filepath.Join(dir, "webproxy-health.json"), []byte(`{"rules":{"rule":{"enabled":false}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := healthConfig(t, dir).RuleHealthChecks["rule"]; got != want {
		t.Fatal("legacy sidecar reimported")
	}
	backup, err := c.PlainJSON()
	if err != nil {
		t.Fatal(err)
	}
	target := healthConfig(t, t.TempDir())
	if err := target.Restore(backup); err != nil {
		t.Fatal(err)
	}
	if got := healthConfig(t, target.Dir()).RuleHealthChecks["rule"]; got != want {
		t.Fatalf("cross-device restore lost checks: %+v", got)
	}
	if err := c.Restore(plain); err != nil {
		t.Fatal(err)
	}
	if got := healthConfig(t, dir).RuleHealthChecks["rule"]; got.Enabled {
		t.Fatal("older backup resurrected a legacy check")
	}
}

func TestHealthChecksRollbackOnSaveFailureAndRejectInvalidBackup(t *testing.T) {
	c := healthConfig(t, t.TempDir())
	want := testHealthConf()
	if err := c.Update(func(c *Config) error {
		c.Settings.AdminPassHash = "hash"
		c.RuleHealthChecks = map[string]HealthCheckConf{"rule": want}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.Dir(), "config.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(func(c *Config) error { delete(c.RuleHealthChecks, "rule"); return nil }); err == nil {
		t.Fatal("expected save failure")
	}
	if c.RuleHealthChecks["rule"] != want {
		t.Fatal("failed save changed in-memory checks")
	}
	if err := os.Remove(filepath.Join(c.Dir(), "config.json.tmp")); err != nil {
		t.Fatal(err)
	}
	backup, _ := c.PlainJSON()
	var incoming Config
	if err := json.Unmarshal(backup, &incoming); err != nil {
		t.Fatal(err)
	}
	bad := want
	bad.IntervalSeconds = 1
	incoming.RuleHealthChecks["rule"] = bad
	backup, _ = json.Marshal(&incoming)
	if err := c.Restore(backup); err == nil {
		t.Fatal("invalid health-check backup accepted")
	}
	if c.RuleHealthChecks["rule"] != want {
		t.Fatal("invalid restore changed checks")
	}
}
