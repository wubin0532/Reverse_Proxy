package upgrade

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManualUpdateInitialState(t *testing.T) {
	m := NewManager("0.1.7", t.TempDir())
	if got := m.Status().State; got != StateIdle {
		t.Fatalf("initial state = %q, want idle", got)
	}
	if m.Version() != "0.1.7" {
		t.Fatalf("version = %q", m.Version())
	}
	m.fail(errors.New("signature invalid"))
	if got := m.Status(); got.State != StateFailed || got.Error == "" {
		t.Fatalf("failed state = %+v", got)
	}
}

func TestRestartStatusIsConfirmedByNewProcess(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(Status{State: StateRestarting, Version: "0.1.8", Note: "restarting"})
	if err := os.WriteFile(filepath.Join(dir, "update-status.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager("0.1.8", dir)
	if m.Status().State != StateRestarting {
		t.Fatalf("恢复状态错误: %+v", m.Status())
	}
	m.MarkStarted()
	if m.Status().State != StateDone {
		t.Fatalf("启动后未确认完成: %+v", m.Status())
	}
	// 重新写入待重启状态检查版本不匹配分支。
	data, _ = json.Marshal(Status{State: StateRestarting, Version: "0.1.9"})
	if err := os.WriteFile(filepath.Join(dir, "update-status.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	mismatch := NewManager("0.1.7", dir)
	if mismatch.Status().State != StateFailed {
		t.Fatalf("版本不匹配未标记失败: %+v", mismatch.Status())
	}
}

func TestManifestVersionValidation(t *testing.T) {
	valid := []string{"0.1.8", "v1.2.3", "1.2.3-rc.1"}
	for _, version := range valid {
		if !versionPattern.MatchString(version) {
			t.Errorf("expected valid: %s", version)
		}
	}
	invalid := []string{"dev", "1.2", "../../bin", "1.2.3;sh"}
	for _, version := range invalid {
		if versionPattern.MatchString(version) {
			t.Errorf("expected invalid: %s", version)
		}
	}
}

func TestStatusReturnsRecoverableStagedInspection(t *testing.T) {
	m := NewManager("0.1.7", t.TempDir())
	m.staged = &stagedPackage{inspection: Inspection{UploadID: "upload-1", Manifest: Manifest{Version: "0.1.8", GOOS: "linux", GOARCH: "arm64"}, Signed: true, Compatible: true}}
	m.state = StateInspected
	status := m.Status()
	if status.Inspection == nil || status.Inspection.UploadID != "upload-1" || status.Version != "0.1.8" {
		t.Fatalf("staged inspection missing from status: %+v", status)
	}
}
