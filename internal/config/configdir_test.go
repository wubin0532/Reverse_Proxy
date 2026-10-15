package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 配置目录加固：拒绝本地用户可写、符号链接或非目录的路径，避免以 root 运行的
// 进程加载攻击者预置的明文 config.json（同时兼作系统目录保护）。
func TestLoadRejectsUnsafeConfigDir(t *testing.T) {
	base := t.TempDir()

	t.Run("除属主外可写", func(t *testing.T) {
		dir := filepath.Join(base, "world-writable")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "可写") {
			t.Fatalf("期望拒绝可写目录, got %v", err)
		}
	})

	t.Run("符号链接", func(t *testing.T) {
		real := filepath.Join(base, "real")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("无法创建符号链接: %v", err)
		}
		if _, err := Load(link); err == nil || !strings.Contains(err.Error(), "符号链接") {
			t.Fatalf("期望拒绝符号链接, got %v", err)
		}
	})

	t.Run("非目录", func(t *testing.T) {
		file := filepath.Join(base, "plain")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(file); err == nil || !strings.Contains(err.Error(), "不是目录") {
			t.Fatalf("期望拒绝非目录路径, got %v", err)
		}
	})
}

// 已存在的目录不允许被 chmod：否则 root 服务会把 /etc/ssl/certs 之类目录改成 0700。
func TestLoadKeepsExistingDirPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("已存在目录权限被改动: %v", info.Mode().Perm())
	}
}
