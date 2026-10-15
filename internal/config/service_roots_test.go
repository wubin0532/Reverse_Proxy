package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceRootConflictRollsBackMutationAndRestore(t *testing.T) {
	base := t.TempDir()
	app, data := filepath.Join(base, "app"), filepath.Join(base, "data")
	for _, dir := range []string{app, data} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(app, alias); err != nil {
		t.Fatal(err)
	}
	c, err := Load(filepath.Join(base, "config"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.State().Close()
	if err := c.Update(func(c *Config) error {
		c.Sites = []Site{{Rules: []SubRule{
			{Type: "cgi", RootDir: app, CGIEntry: "app.cgi", BasicAuth: true},
			{Type: "webdav", RootDir: data, WebDAVWritable: true, BasicAuth: true},
		}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := c.PlainJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Update(func(c *Config) error { c.Sites[0].Rules[1].RootDir = alias; return nil }); err == nil {
		t.Fatal("unsafe mutation accepted")
	}
	var imported Config
	if err := json.Unmarshal(before, &imported); err != nil {
		t.Fatal(err)
	}
	imported.Sites[0].Rules[1].RootDir = alias
	backup, err := json.Marshal(&imported)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Restore(backup); err == nil {
		t.Fatal("unsafe backup accepted")
	}
	after, err := c.PlainJSON()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected change modified the active configuration:", err)
	}
	disk, err := Load(c.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer disk.State().Close()
	persisted, err := disk.PlainJSON()
	if err != nil || !bytes.Equal(before, persisted) {
		t.Fatal("rejected change modified persisted configuration:", err)
	}
}

func TestServiceRootsRejectRealDirectoryOverlap(t *testing.T) {
	base := t.TempDir()
	app := filepath.Join(base, "app")
	data := filepath.Join(base, "data")
	for _, dir := range []string{app, data, filepath.Join(app, "uploads")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(app, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, dav, cgi string
	}{
		{"same directory", app, app},
		{"DAV alias", alias, app},
		{"CGI alias", app, alias},
		{"nested CGI", alias, filepath.Join(app, "future")},
		{"nested DAV", filepath.Join(alias, "uploads"), app},
		{"missing nested DAV", filepath.Join(alias, "future", "uploads"), app},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Sites: []Site{{Rules: []SubRule{
				{Type: "cgi", RootDir: tc.cgi, CGIEntry: "app.cgi", BasicAuth: true},
				{Type: "webdav", RootDir: tc.dav, WebDAVWritable: true, BasicAuth: true},
			}}}}
			if err := c.NormalizeWorkspace(); err == nil {
				t.Fatal("overlapping real service roots accepted")
			}
		})
	}
	// Read-only sharing does not grant the ability to modify CGI code.
	c := Config{Sites: []Site{{Rules: []SubRule{
		{Type: "cgi", RootDir: app, CGIEntry: "app.cgi", BasicAuth: true},
		{Type: "webdav", RootDir: alias},
		{Type: "webdav", RootDir: data, WebDAVWritable: true, BasicAuth: true},
	}}}}
	if err := c.NormalizeWorkspace(); err != nil {
		t.Fatal("disjoint writable directory or read-only sharing rejected:", err)
	}
}

func TestServiceRootsPersistCanonicalPathsAndRejectUnsafeAliases(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "data")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	c := Config{Sites: []Site{{Rules: []SubRule{{Type: "webdav", RootDir: filepath.Join(alias, "future")}}}}}
	if err := c.NormalizeWorkspace(); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Sites[0].Rules[0].RootDir; got != filepath.Join(canonical, "future") {
		t.Fatal("unresolved alias retained in configuration:", got)
	}
	for _, tc := range []struct{ name, target string }{{"system root", "/"}, {"dangling", filepath.Join(base, "missing")}} {
		t.Run(tc.name, func(t *testing.T) {
			link := filepath.Join(base, tc.name)
			if err := os.Symlink(tc.target, link); err != nil {
				t.Fatal(err)
			}
			c := Config{Sites: []Site{{Rules: []SubRule{{Type: "webdav", RootDir: link}}}}}
			if err := c.NormalizeWorkspace(); err == nil {
				t.Fatal("unsafe root alias accepted")
			}
		})
	}
}
