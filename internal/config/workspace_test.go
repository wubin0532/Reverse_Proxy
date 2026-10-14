package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestWorkspaceRollbackRestoreAndReferenceValidation(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	policy := ProtectionPolicy{ID: "p", Name: "local", Enabled: true, IPListMode: "blacklist", IPList: []string{"203.0.113.9"}}
	if err = c.Update(func(c *Config) error {
		c.Settings.AdminPassHash = "fixture"
		c.ProtectionPolicies = []ProtectionPolicy{policy}
		c.Sites = []Site{{ID: "s", PolicyID: "p"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backup, _ := c.PlainJSON()
	if err = c.Update(func(c *Config) error { c.ProtectionPolicies = nil; return nil }); err == nil {
		t.Fatal("referenced policy deletion should fail")
	}
	if len(c.ProtectionPolicies) != 1 || c.SchemaVersion != 2 {
		t.Fatal("new fields lost on rollback")
	}
	if err = c.Update(func(c *Config) error { c.Sites = nil; c.ProtectionPolicies = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = c.Restore(backup); err != nil {
		t.Fatal(err)
	}
	if len(c.ProtectionPolicies) != 1 || c.Sites[0].PolicyID != "p" {
		t.Fatal("bindings lost on restore")
	}
	var future map[string]interface{}
	json.Unmarshal(backup, &future)
	future["schemaVersion"] = 3
	data, _ := json.Marshal(future)
	if c.Restore(data) == nil {
		t.Fatal("future schema must not silently lose fields")
	}
}

func TestLegacyWorkspaceDoesNotInventBindings(t *testing.T) {
	c := Config{DDNS: []DDNSTask{{ID: "old", Enabled: true, IPSource: "webhook"}}, Sites: []Site{{ID: "site", Rules: []SubRule{{ID: "rule"}}}}}
	if err := c.NormalizeWorkspace(); err != nil {
		t.Fatal(err)
	}
	if c.DDNS[0].Enabled || c.DDNS[0].IPSource != "interface" || c.Sites[0].PolicyID != "" || c.Sites[0].Rules[0].PolicyID != "" {
		t.Fatal("legacy migration changed admission behavior")
	}
}

func TestWorkspaceRejectsInvalidNewFieldsOnRestore(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.Settings.AdminPassHash = "fixture"
	cases := []*Config{
		{DDNS: []DDNSTask{{Records: []DDNSRecord{{ID: "r", Domain: "home.example.com", Type: "TXT"}}}}},
		{DDNS: []DDNSTask{{IPv6Source: &IPSourceConfig{Source: "command"}}}},
		{Certs: []CertConf{{Source: "sync"}}},
		{Sites: []Site{{DefaultResponse: &DefaultResponse{StatusCode: 302}}}},
	}
	for _, incoming := range cases {
		incoming.Settings.AdminPassHash = "fixture"
		data, _ := json.Marshal(incoming)
		if c.Restore(data) == nil {
			t.Fatalf("invalid import accepted: %+v", incoming)
		}
	}
}
