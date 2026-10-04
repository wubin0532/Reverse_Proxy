package tunnel

import (
	"andey-proxy/internal/config"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type runtimeTransport func(*http.Request) (*http.Response, error)

func (f runtimeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testRelease(data []byte) Release {
	sum := sha256.Sum256(data)
	return Release{ID: "release", Version: "2026.9.0", Asset: "cloudflared-linux-amd64", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), URL: "https://github.com/cloudflare/cloudflared/releases/download/2026.9.0/cloudflared-linux-amd64"}
}
func installFixture(t *testing.T, data []byte) (*Manager, Release) {
	t.Helper()
	m, _, _ := newTestManager(t)
	r := testRelease(data)
	m.releases.cached, m.releases.checked = r, time.Now()
	m.releases.client.Transport = runtimeTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Header: http.Header{}}, nil
	})
	m.runtime = func() Runtime { return Runtime{GOOS: "linux", GOARCH: "amd64", Compatible: true} }
	m.verifyBinary = func(path, version string) error {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, data) || version != r.Version {
			return errors.New("invalid candidate")
		}
		return os.Chmod(path, 0o700)
	}
	m.freeSpace = func(string) (uint64, error) { return 100 << 20, nil }
	return m, r
}
func TestRuntimeAssetsAndTrustedOrigins(t *testing.T) {
	for _, tc := range []struct{ os, arch, asset string }{{"linux", "amd64", "cloudflared-linux-amd64"}, {"linux", "arm64", "cloudflared-linux-arm64"}, {"linux", "arm", "cloudflared-linux-arm"}, {"linux", "mips", ""}, {"linux", "mipsle", ""}, {"darwin", "arm64", ""}} {
		if got := runtimeAsset(tc.os, tc.arch); got != tc.asset {
			t.Fatalf("%s/%s: %s", tc.os, tc.arch, got)
		}
	}
	for _, tc := range []struct {
		url     string
		allowed bool
	}{{"https://github.com/cloudflare/cloudflared/releases/download/2026.9.0/file", true}, {"https://release-assets.githubusercontent.com/file", true}, {"http://github.com/file", false}, {"https://github.com.evil.invalid/file", false}, {"https://user:password@github.com/file", false}, {"https://github.com:8443/file", false}, {"https://127.0.0.1/file", false}} {
		u, _ := url.Parse(tc.url)
		if officialDownloadURL(u) != tc.allowed {
			t.Fatal(tc.url)
		}
	}
}
func TestReleaseCatalogValidatesOfficialMetadata(t *testing.T) {
	for _, tc := range []struct {
		name                string
		status              int
		tag, digest         string
		size                int64
		prerelease, missing bool
		fail                bool
	}{
		{"stable", 200, "2026.9.0", "sha256:" + strings.Repeat("a", 64), 40 << 20, false, false, false},
		{"rate limit", 429, "", "", 0, false, false, true}, {"forbidden", 403, "", "", 0, false, false, true},
		{"prerelease", 200, "2026.9.0", "sha256:" + strings.Repeat("a", 64), 40 << 20, true, false, true},
		{"unsafe tag", 200, "../2026.9.0", "sha256:" + strings.Repeat("a", 64), 40 << 20, false, false, true},
		{"no digest", 200, "2026.9.0", "", 40 << 20, false, false, true},
		{"oversize", 200, "2026.9.0", "sha256:" + strings.Repeat("a", 64), 81 << 20, false, false, true},
		{"no asset", 200, "2026.9.0", "sha256:" + strings.Repeat("a", 64), 40 << 20, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				asset := "cloudflared-linux-amd64"
				if tc.missing {
					asset = "cloudflared-linux-arm64"
				}
				json.NewEncoder(w).Encode(map[string]any{"tag_name": tc.tag, "prerelease": tc.prerelease, "assets": []any{map[string]any{"name": asset, "size": tc.size, "digest": tc.digest}}})
			}))
			defer server.Close()
			c := newReleaseCatalog()
			c.endpoint = server.URL
			c.client = server.Client()
			release, err := c.fetch(context.Background(), "cloudflared-linux-amd64")
			if (err != nil) != tc.fail {
				t.Fatalf("%+v %v", release, err)
			}
			if !tc.fail {
				c.cached = release
				c.checked = time.Now()
				if _, err = c.selected(release.ID); err != nil {
					t.Fatal(err)
				}
				c.checked = time.Now().Add(-2 * time.Hour)
				if _, err = c.selected(release.ID); err == nil {
					t.Fatal("expired selection accepted")
				}
			}
		})
	}
}
func TestStreamingDownloadRejectsIncompleteAndUntrustedContent(t *testing.T) {
	data := []byte("official binary fixture")
	for _, kind := range []string{"success", "checksum", "truncated", "content length", "network", "redirect", "untrusted"} {
		t.Run(kind, func(t *testing.T) {
			c := newReleaseCatalog()
			r := testRelease(data)
			body := data
			length := int64(len(data))
			calls := 0
			if kind == "checksum" {
				r.SHA256 = strings.Repeat("0", 64)
			}
			if kind == "truncated" {
				body = body[:3]
				length = -1
			}
			if kind == "content length" {
				length++
			}
			if kind == "untrusted" {
				r.URL = "https://example.com/file"
			}
			c.client.Transport = runtimeTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if kind == "network" {
					return nil, errors.New("disconnected")
				}
				if kind == "redirect" {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: length, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			var progress int64
			path := filepath.Join(t.TempDir(), "candidate")
			err := c.download(context.Background(), r, path, func(n int64) { progress = n })
			if (err == nil) != (kind == "success") {
				t.Fatalf("unexpected result %v", err)
			}
			if kind == "success" {
				info, _ := os.Stat(path)
				if info.Mode().Perm() != 0o600 || progress != r.Size {
					t.Fatal("unsafe permissions or lost progress")
				}
			}
			if kind == "redirect" && calls != 1 || kind == "untrusted" && calls != 0 {
				t.Fatal("untrusted source requested")
			}
		})
	}
}
func TestRuntimeInstallPreflightAndCommit(t *testing.T) {
	for _, kind := range []string{"success", "disk", "checksum", "architecture", "interrupted", "concurrent"} {
		t.Run(kind, func(t *testing.T) {
			m, r := installFixture(t, []byte("new managed binary"))
			if err := prepareRuntimeDir(m.cfg.Dir()); err != nil {
				t.Fatal(err)
			}
			target := managedRuntimePath(m.cfg.Dir())
			os.WriteFile(target, []byte("old managed binary"), 0o700)
			if kind == "disk" {
				m.freeSpace = func(string) (uint64, error) { return 0, nil }
			}
			if kind == "checksum" {
				r.SHA256 = strings.Repeat("0", 64)
				m.releases.cached = r
			}
			if kind == "architecture" {
				m.verifyBinary = func(string, string) error { return errors.New("wrong architecture") }
			}
			if kind == "interrupted" {
				m.cancel()
			}
			if kind == "concurrent" {
				m.mutation.Lock()
				defer m.mutation.Unlock()
			}
			op, err := m.installRuntime(r.ID, false, nil)
			if kind == "interrupted" || kind == "concurrent" {
				if err == nil {
					t.Fatal("started blocked operation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { state, _ := m.cfg.State().TunnelOperation(op.ID); return state.Status != "running" })
			saved, _ := m.cfg.State().TunnelOperation(op.ID)
			got, _ := os.ReadFile(target)
			if kind == "success" {
				if saved.Status != "succeeded" || string(got) != "new managed binary" {
					t.Fatalf("%+v %s", saved, got)
				}
			} else if saved.Status != "failed" || string(got) != "old managed binary" {
				t.Fatalf("%+v %s", saved, got)
			}
			leftovers, _ := filepath.Glob(filepath.Join(runtimeDir(m.cfg.Dir()), ".download-*"))
			if len(leftovers) != 0 {
				t.Fatal("download remnants")
			}
		})
	}
}
func TestRuntimeRecoveryRestoresOrKeepsCommittedBinary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		had, committed bool
		want           string
	}{{"update", true, false, "old"}, {"fresh", false, false, ""}, {"committed", true, true, "new"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			prepareRuntimeDir(dir)
			path := managedRuntimePath(dir)
			os.WriteFile(path, []byte("new"), 0o700)
			if tc.had {
				os.WriteFile(path+".previous", []byte("old"), 0o700)
			}
			saveRuntimeJournal(dir, runtimeJournal{HadManaged: tc.had, Committed: tc.committed})
			os.WriteFile(filepath.Join(runtimeDir(dir), ".download-orphan"), []byte("partial"), 0o600)
			if err := recoverRuntime(dir); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			if string(data) != tc.want {
				t.Fatalf("got %q", data)
			}
			if _, err := os.Stat(path + ".previous"); !os.IsNotExist(err) {
				t.Fatal("backup retained")
			}
			if err := recoverRuntime(dir); err != nil {
				t.Fatal("non-idempotent recovery")
			}
		})
	}
	dir := t.TempDir()
	prepareRuntimeDir(dir)
	os.WriteFile(filepath.Join(runtimeDir(dir), "install-state.json"), []byte("{}"), 0o600)
	if recoverRuntime(dir) == nil {
		t.Fatal("malformed journal accepted")
	}
	if err := verifyRuntimeBinary(filepath.Join(dir, "absent"), "2026.9.0"); err == nil {
		t.Fatal("invalid binary accepted")
	}
}
func TestRuntimeUpdateFailureRestoresProcessAndEnabledState(t *testing.T) {
	setupHelper(t)
	t.Setenv("ANDEY_HELPER_FAIL_MANAGED", "1")
	exe, _ := os.Executable()
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	m, r := installFixture(t, data)
	m.runtime = func() Runtime { rt := detectManagedRuntime(m.cfg.Dir()); rt.GOOS = "linux"; return rt }
	inst, _ := m.instance("instance")
	inst.Enabled = true
	m.saveInstance(inst)
	m.Reload()
	eventually(t, func() bool { return m.Status(inst.ID).Ready })
	if _, err = m.installRuntime(r.ID, false, nil); err == nil {
		t.Fatal("missing restart confirmation accepted")
	}
	op, err := m.installRuntime(r.ID, true, []string{inst.ID})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { state, _ := m.cfg.State().TunnelOperation(op.ID); return state.Status == "failed" })
	eventually(t, func() bool { return m.Status(inst.ID).Ready })
	inst, _ = m.instance(inst.ID)
	if !inst.Enabled || m.runtime().Source != "system" {
		t.Fatal("old source or enabled state lost")
	}
	state, _ := m.cfg.State().TunnelOperation(op.ID)
	if !strings.Contains(state.Error, "新程序启动失败") {
		t.Fatal(fmt.Sprintf("%+v", state))
	}
}

func TestRuntimeBinaryChecksNativeArchitectureAndVersion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("verified by Linux device helper")
	}
	setupHelper(t)
	source, _ := os.Executable()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cloudflared")
	os.WriteFile(path, data, 0o600)
	if err = verifyRuntimeBinary(path, "2026.9.0"); err != nil {
		t.Fatal(err)
	}
	if err = verifyRuntimeBinary(path, "2026.10.0"); err == nil {
		t.Fatal("wrong version accepted")
	}
	// ELF e_machine has a fixed offset in both 32-bit and 64-bit headers.
	data[18], data[19] = 0xff, 0xff
	os.WriteFile(path, data, 0o600)
	if err = verifyRuntimeBinary(path, "2026.9.0"); err == nil {
		t.Fatal("wrong architecture accepted")
	}
}
func TestReleaseCacheAndActiveRefresh(t *testing.T) {
	asset := runtimeAsset(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		t.Skip("automatic download is Linux only")
	}
	count := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "2026.9.0", "assets": []any{map[string]any{"name": asset, "size": 10, "digest": "sha256:" + strings.Repeat("a", 64)}}})
	}))
	defer server.Close()
	c := newReleaseCatalog()
	c.endpoint = server.URL
	c.client = server.Client()
	first, err := c.latest(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	c.attempted = time.Now().Add(-11 * time.Second)
	second, err := c.latest(context.Background(), false)
	if err != nil || count != 1 || second.ID != first.ID {
		t.Fatal("cache miss")
	}
	if _, err = c.latest(context.Background(), true); err != nil || count != 2 {
		t.Fatal("manual refresh skipped")
	}
	c.checked = time.Now().Add(-2 * time.Hour)
	c.attempted = time.Now().Add(-11 * time.Second)
	if _, err = c.latest(context.Background(), false); err != nil || count != 3 {
		t.Fatal("cache did not expire")
	}
}

func TestInterruptedDownloadBeforeSwitchIsCleanedOnStartup(t *testing.T) {
	m, _, _ := newTestManager(t)
	dir := m.cfg.Dir()
	if err := prepareRuntimeDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runtimeDir(dir), ".download-interrupted")
	os.WriteFile(path, []byte("partial"), 0o600)
	m.recordOperation(config.TunnelOperation{ID: "interrupted", Kind: "runtime-install", Status: "running", Stage: "downloadRuntime"})
	m.Start()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("partial download survived startup")
	}
	op, _ := m.cfg.State().TunnelOperation("interrupted")
	if op.Status != "interrupted" || !strings.Contains(op.Error, "程序安装") {
		t.Fatalf("%+v", op)
	}
}
