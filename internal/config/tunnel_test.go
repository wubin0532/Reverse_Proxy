package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTunnelSecretsEncryptedAndRestoreDisablesInstances(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conf")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.State().Close()
	c.Update(func(c *Config) error {
		c.Settings.AdminPassHash = "hash"
		c.TunnelAccounts = []TunnelAccount{{ID: "account", Token: "API-CANARY"}}
		c.Tunnels = []TunnelInstance{{ID: "instance", Token: "TUNNEL-CANARY", Enabled: true, Routes: []TunnelRoute{{ID: "route", DNSOwned: true, DNSRecordID: "record"}}}}
		return nil
	})
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if bytes.Contains(raw, []byte("CANARY")) {
		t.Fatal("plaintext credentials persisted")
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.State().Close()
	if !loaded.Tunnels[0].Enabled || loaded.TunnelAccounts[0].Token != "API-CANARY" {
		t.Fatal("load lost tunnel fields")
	}
	plain, _ := c.PlainJSON()
	if err = c.Restore(plain); err != nil {
		t.Fatal(err)
	}
	if c.Tunnels[0].Enabled || !c.Tunnels[0].Routes[0].DNSOwned || c.Tunnels[0].Token != "TUNNEL-CANARY" {
		t.Fatal("restore did not preserve configuration or disable instance")
	}
}
func TestTunnelUpdateRollbackIncludesNewCollections(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "conf"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.State().Close()
	c.Update(func(c *Config) error {
		c.TunnelAccounts = []TunnelAccount{{ID: "account", Token: "original"}}
		c.Tunnels = []TunnelInstance{{ID: "instance", Name: "original"}}
		return nil
	})
	err = c.Update(func(c *Config) error {
		c.TunnelAccounts[0].Token = "changed"
		c.Tunnels[0].Name = "changed"
		return errors.New("reject")
	})
	if err == nil || c.TunnelAccounts[0].Token != "original" || c.Tunnels[0].Name != "original" {
		t.Fatal("rollback lost collections")
	}
	old := c.filePath
	c.filePath = filepath.Join(c.Dir(), "missing", "config.json")
	err = c.Update(func(c *Config) error { c.Tunnels = nil; return nil })
	c.filePath = old
	if err == nil || len(c.Tunnels) != 1 {
		t.Fatal("persistence failure did not roll back")
	}
}

func TestPublishedReferencesRecognizeOnlyKnownLocalSockets(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	cfg.Sites = []Site{{ID: "old"}, {ID: "new"}}
	cfg.Tunnels = []TunnelInstance{{ID: "test", Routes: []TunnelRoute{{Target: "site", SiteID: "new", Hostname: "draft.example.com", AppliedHostname: "old.example.com", AppliedService: "unix:" + TunnelSocketPath(cfg.Dir(), "old"), PendingHostname: "pending.example.com", PendingService: "unix:/tmp/unknown.sock"}}}}
	if !cfg.TunnelReferencedSite("old") || !cfg.TunnelReferencedSite("new") || cfg.TunnelReferencedSite("unknown") {
		t.Fatal("incorrect reference lifecycle")
	}
	bindings := cfg.TunnelSiteBindings(false)
	if !bindings["old"]["old.example.com"] || bindings["old"]["pending.example.com"] {
		t.Fatal("unknown socket was trusted")
	}
	cfg.Tunnels[0].Routes[0].AppliedService = "unix:" + TunnelSocketPath(cfg.Dir(), "old") + "/../other.sock"
	if cfg.TunnelReferencedSite("old") {
		t.Fatal("non-exact socket path recognized")
	}
}
