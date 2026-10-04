package tunnel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

// Run this same test executable as a fake cloudflared, without a shell dependency.
func init() {
	if os.Getenv("ANDEY_HELPER") != "1" {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("cloudflared version " + os.Getenv("ANDEY_HELPER_VERSION"))
		os.Exit(0)
	}
	if os.Getenv("ANDEY_HELPER_FAIL_MANAGED") == "1" && filepath.Base(filepath.Dir(os.Args[0])) == "runtime" {
		os.Exit(7)
	}
	var metrics, path string
	for i, arg := range os.Args {
		if i+1 < len(os.Args) {
			if arg == "--metrics" {
				metrics = os.Args[i+1]
			}
			if arg == "--token-file" {
				path = os.Args[i+1]
			}
		}
	}
	info, _ := os.Stat(path)
	mode := uint32(0)
	if info != nil {
		mode = uint32(info.Mode().Perm())
	}
	data, _ := json.Marshal(map[string]any{"args": os.Args[1:], "path": path, "mode": mode})
	_ = os.WriteFile(os.Getenv("ANDEY_HELPER_OUTPUT"), data, 0o600)
	if os.Getenv("ANDEY_HELPER_CRASH") == "1" {
		f, _ := os.OpenFile(os.Getenv("ANDEY_HELPER_RUNS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if f != nil {
			f.WriteString("run\n")
			f.Close()
		}
		os.Exit(7)
	}
	http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"connectorId":"local-connector","readyConnections":4}`)
	})
	token, _ := os.ReadFile(path)
	fmt.Printf("{\"level\":\"info\",\"message\":\"token=%s API-SUPER-SECRET\"}\n", token)
	_ = http.ListenAndServe(metrics, nil)
	os.Exit(0)
}
func setupHelper(t *testing.T) {
	t.Helper()
	// Race-instrumented helper exits otherwise sleep for a second, unlike the
	// actual CLI. Disable that exit-only delay without disabling race detection.
	options := os.Getenv("GORACE") + " atexit_sleep_ms=0"
	t.Setenv("GORACE", options)
	t.Setenv("ANDEY_HELPER", "1")
	t.Setenv("ANDEY_HELPER_VERSION", "2026.9.0")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.Symlink(exe, filepath.Join(dir, "cloudflared")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANDEY_HELPER_OUTPUT", filepath.Join(dir, "output.json"))
	t.Setenv("ANDEY_HELPER_RUNS", filepath.Join(dir, "runs"))
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func TestProcessReadyTokenPermissionsAndCleanup(t *testing.T) {
	setupHelper(t)
	m, _, _ := newTestManager(t)
	runtime := DetectRuntime()
	if !runtime.Compatible || runtime.Version != "2026.9.0" {
		t.Fatalf("%+v", runtime)
	}
	center, err := logcenter.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logcenter.SetDefault(center)
	defer func() { logcenter.SetDefault(nil); center.Close() }()
	inst, _ := m.instance("instance")
	inst.Enabled = true
	m.saveInstance(inst)
	m.Reload()
	eventually(t, func() bool { return m.Status(inst.ID).Ready })
	data, err := os.ReadFile(os.Getenv("ANDEY_HELPER_OUTPUT"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Args []string `json:"args"`
		Path string   `json:"path"`
		Mode uint32   `json:"mode"`
	}
	json.Unmarshal(data, &saved)
	if saved.Mode != 0o600 || strings.Contains(strings.Join(saved.Args, " "), inst.Token) {
		t.Fatalf("unsafe command: %s", data)
	}
	entries, _ := center.Query(logcenter.Query{Source: "tunnel", Limit: 100})
	for _, entry := range entries {
		if strings.Contains(entry.Message, "SUPER-SECRET") || strings.Contains(entry.Message, inst.Token) {
			t.Fatal("secret leaked in process log")
		}
	}
	inst.Enabled = false
	m.saveInstance(inst)
	m.Reload()
	if m.Status(inst.ID).Process != "stopped" {
		t.Fatal("worker still running")
	}
	if _, err = os.Stat(saved.Path); !os.IsNotExist(err) {
		t.Fatal("token file retained")
	}
}
func TestProcessCrashRestartsAndExplicitStopPreventsRestart(t *testing.T) {
	setupHelper(t)
	t.Setenv("ANDEY_HELPER_CRASH", "1")
	m, _, _ := newTestManager(t)
	inst, _ := m.instance("instance")
	inst.Enabled = true
	m.saveInstance(inst)
	m.Reload()
	eventually(t, func() bool {
		data, _ := os.ReadFile(os.Getenv("ANDEY_HELPER_RUNS"))
		return strings.Count(string(data), "run") >= 2
	})
	inst.Enabled = false
	m.saveInstance(inst)
	m.Reload()
	before, _ := os.ReadFile(os.Getenv("ANDEY_HELPER_RUNS"))
	time.Sleep(1200 * time.Millisecond)
	after, _ := os.ReadFile(os.Getenv("ANDEY_HELPER_RUNS"))
	if string(before) != string(after) {
		t.Fatal("explicit stop restarted")
	}
}
func TestRuntimeOldVersionIsRejected(t *testing.T) {
	setupHelper(t)
	t.Setenv("ANDEY_HELPER_VERSION", "2025.3.9")
	if DetectRuntime().Compatible {
		t.Fatal("old cloudflared accepted")
	}
}
func TestInterruptedOperationSurvivesRestart(t *testing.T) {
	m, _, _ := newTestManager(t)
	op := config.TunnelOperation{ID: "old", InstanceID: "instance", Kind: "sync", Status: "running", Stage: "writeDNS"}
	if err := m.recordOperation(op); err != nil {
		t.Fatal(err)
	}
	m.Start()
	saved, _ := m.cfg.State().TunnelOperation(op.ID)
	if saved.Status != "interrupted" || saved.Stage != "writeDNS" {
		t.Fatalf("%+v", saved)
	}
}
