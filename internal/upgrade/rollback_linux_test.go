//go:build linux

package upgrade

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunningExecutableRollbackHelper(t *testing.T) {
	mode := os.Getenv("ANDEY_ROLLBACK_HELPER")
	if mode == "" {
		return
	}
	exe, err := executablePath()
	if err != nil {
		t.Fatal(err)
	}
	before, err := hashFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("ANDEY_ROLLBACK_DIR")
	candidate := filepath.Join(dir, "candidate")
	if err = os.WriteFile(candidate, []byte("new program placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewManager("0.3.5", dir)
	m.restart = func() error { return errors.New("injected restart failure") }
	if mode == "state" {
		m.statusPath = filepath.Join(dir, "missing", "status.json")
	}
	m.staged = &stagedPackage{binaryPath: candidate, path: filepath.Join(dir, "payload.run"), inspection: Inspection{UploadID: "fixture", Manifest: Manifest{Version: "0.3.6"}}, expires: time.Now().Add(time.Minute)}
	err = m.Install("fixture", false)
	if mode == "state" && err == nil {
		t.Fatal("state persistence should fail")
	}
	if mode == "restart" {
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(4 * time.Second)
		for m.Status().State != StateFailed && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if m.Status().State != StateFailed {
		t.Fatalf("%+v", m.Status())
	}
	if !strings.Contains(m.Status().Error, "失败") {
		t.Fatal("missing failure detail")
	}
	after, err := hashFile(exe)
	if err != nil || before != after {
		t.Fatalf("wrong path restored: %v", err)
	}
	if _, err = os.Stat(exe + ".bak.bak"); !os.IsNotExist(err) {
		t.Fatal("derived rollback path")
	}
	fmt.Println("fixed executable restored", mode)
}
func hashFile(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}
func TestLinuxRollbackWhileProgramIsRenamed(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"state", "restart"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "andey-proxy")
			src, err := os.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			dst, err := os.OpenFile(exe, os.O_CREATE|os.O_WRONLY, 0o755)
			if err != nil {
				src.Close()
				t.Fatal(err)
			}
			_, err = io.Copy(dst, src)
			src.Close()
			dst.Close()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "-test.run=^TestRunningExecutableRollbackHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "ANDEY_ROLLBACK_HELPER="+mode, "ANDEY_ROLLBACK_DIR="+dir, "GORACE=atexit_sleep_ms=0")
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "fixed executable restored") {
				t.Fatalf("%s %v", out, err)
			}
		})
	}
}
