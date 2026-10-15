package webproxy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"andey-proxy/internal/config"
)

func TestWebDAVAliasRetargetCannotModifyCGICode(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("CGI execution unavailable")
	}
	base := t.TempDir()
	app, data := filepath.Join(base, "app"), filepath.Join(base, "data")
	for _, dir := range []string{app, data} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(app, "app.cgi"), []byte("#!/bin/sh\n. ./helper.sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	original := "printf 'Content-Type: text/plain\\n\\nORIGINAL'\n"
	if err := os.WriteFile(filepath.Join(app, "helper.sh"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "dav-alias")
	if err := os.Symlink(data, alias); err != nil {
		t.Fatal(err)
	}
	cgi := config.SubRule{ID: "cgi", Name: "CGI", Type: "cgi", Enabled: true, RootDir: app, CGIEntry: "app.cgi", FrontendPath: "/cgi", BasicAuth: true, AuthUser: "fixture", AuthPass: "secret"}
	cfg, svc, u := serviceFixture(t, cgi)
	dav := config.SubRule{ID: "dav", Name: "DAV", Type: "webdav", Enabled: true, RootDir: alias, FrontendPath: "/dav", WebDAVWritable: true, BasicAuth: true, AuthUser: "fixture", AuthPass: "secret"}
	if err := cfg.Update(func(c *config.Config) error {
		c.Sites[0].Rules = append(c.Sites[0].Rules, dav)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Retarget after save but before reload or the first WebDAV request.
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(app, alias); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if code, _, body := requestService(t, "PUT", u+"/dav/helper.sh", "printf 'Content-Type: text/plain\\n\\nREPLACED'\n", true, nil); code != 201 {
		t.Fatal("independent DAV directory stopped working:", code, body)
	}
	if code, _, body := requestService(t, "GET", u+"/cgi/", "", true, nil); code != 200 || body != "ORIGINAL" {
		t.Fatal("DAV alias changed CGI behavior:", code, body)
	}
	if got, err := os.ReadFile(filepath.Join(app, "helper.sh")); err != nil || string(got) != original {
		t.Fatal("CGI source changed:", err, string(got))
	}
}

func TestServiceDirectoryReplacementBeforeFirstRequestIsRejected(t *testing.T) {
	for _, kind := range []string{"webdav", "cgi"} {
		for _, replacement := range []string{"symlink", "directory"} {
			t.Run(kind+"/"+replacement, func(t *testing.T) {
				base := t.TempDir()
				root, other := filepath.Join(base, "root"), filepath.Join(base, "other")
				for _, dir := range []string{root, other} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				rule := config.SubRule{ID: "service", Name: kind, Type: kind, Enabled: true, RootDir: root, CGIEntry: "app.cgi", WebDAVWritable: true, BasicAuth: true, AuthUser: "fixture", AuthPass: "secret"}
				cfg, svc, u := serviceFixture(t, rule)
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				if replacement == "symlink" {
					if err := os.Symlink(other, root); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "app.cgi"), []byte("#!/bin/sh\nprintf 'Content-Type: text/plain\\n\\nREPLACEMENT'\n"), 0700); err != nil {
					t.Fatal(err)
				}
				for _, reload := range []bool{false, true} {
					if reload {
						if err := svc.Reload(); err != nil {
							t.Fatal(err)
						}
					}
					method, body := "PUT", "must not write"
					if kind == "cgi" {
						method, body = "GET", ""
					}
					if code, _, response := requestService(t, method, u+"/file", body, true, nil); code != 503 {
						t.Fatal("changed directory was used:", code, response)
					}
				}
				if _, err := os.Stat(filepath.Join(root, "file")); !os.IsNotExist(err) {
					t.Fatal("request modified replacement directory:", err)
				}
				// A deliberate rule edit rebinds a newly provisioned, real directory.
				if kind == "webdav" && replacement == "directory" {
					if err := cfg.Update(func(c *config.Config) error { c.Sites[0].Rules[0].Name += " rebound"; return nil }); err != nil {
						t.Fatal(err)
					}
					if err := svc.Reload(); err != nil {
						t.Fatal(err)
					}
					if code, _, response := requestService(t, "PUT", u+"/file", "new directory", true, nil); code != 201 {
						t.Fatal("explicit rebind failed:", code, response)
					}
				}
			})
		}
	}
}
