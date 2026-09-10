package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateStoreUpdateFlushClose(t *testing.T) {
	dir := t.TempDir()
	st := LoadState(dir)
	st.writeDelay = time.Hour // 关掉去抖定时器对用例的干扰，只测显式落盘
	st.Update(func(s *State) {
		s.TOTPLastCounter = 42
		s.Certs["c1"] = CertState{CertFile: "certs/c1.crt", KeyFile: "certs/c1.key", NotAfter: "2030-01-01T00:00:00Z"}
	})
	// 未 Flush 前不落盘
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("去抖窗口内不应落盘")
	}
	if got := st.TOTPCounter(); got != 42 {
		t.Fatalf("TOTPCounter = %d, want 42", got)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state.json 权限: %v", fi.Mode().Perm())
	}
	// 重新加载应读回
	st2 := LoadState(dir)
	if got := st2.TOTPCounter(); got != 42 {
		t.Fatalf("重载后 TOTPCounter = %d, want 42", got)
	}
	if cs := st2.Cert("c1"); cs.NotAfter != "2030-01-01T00:00:00Z" || cs.CertFile == "" {
		t.Fatalf("重载后证书状态丢失: %+v", cs)
	}
	// Close 落盘待写变更
	st2.Update(func(s *State) { s.Certs["c2"] = CertState{LastError: "boom"} })
	if err := st2.Close(); err != nil {
		t.Fatal(err)
	}
	st3 := LoadState(dir)
	if cs := st3.Cert("c2"); cs.LastError != "boom" {
		t.Fatal("Close 未落盘待写变更")
	}
	// Close 后 Update 不再排期写盘，但 Flush 仍可用
	st3.Update(func(s *State) { s.TOTPLastCounter = 43 })
	if err := st3.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(dir).TOTPCounter(); got != 43 {
		t.Fatalf("Close 后 Flush = %d, want 43", got)
	}
}

func TestStateStoreDebounceCoalescesWrites(t *testing.T) {
	dir := t.TempDir()
	st := LoadState(dir)
	st.writeDelay = 30 * time.Millisecond
	for i := 0; i < 10; i++ {
		i := i
		st.Update(func(s *State) { s.TOTPLastCounter = int64(i) })
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
			var s State
			if json.Unmarshal(data, &s) == nil && s.TOTPLastCounter == 9 {
				return // 去抖后合并为一次写盘且内容为最终值
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("去抖写盘超时或内容不是最终值")
}

func TestStateStoreSeedDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	st := LoadState(dir)
	st.Update(func(s *State) {
		s.TOTPLastCounter = 100
		s.Certs["c1"] = CertState{NotAfter: "2031-01-01T00:00:00Z"}
	})
	legacy := []byte(`{"settings":{"totpLastCounter":5},"certs":[{"id":"c1","notAfter":"2020-01-01T00:00:00Z"},{"id":"c2","certFile":"certs/c2.crt","lastError":"old"}]}`)
	if err := st.seedRuntimeState(legacy); err != nil {
		t.Fatal(err)
	}
	if got := st.TOTPCounter(); got != 100 {
		t.Fatalf("已有计数器被旧值覆盖: %d", got)
	}
	if cs := st.Cert("c1"); cs.NotAfter != "2031-01-01T00:00:00Z" {
		t.Fatalf("已有证书状态被旧值覆盖: %+v", cs)
	}
	if cs := st.Cert("c2"); cs.CertFile != "certs/c2.crt" || cs.LastError != "old" {
		t.Fatalf("缺失条目未被填充: %+v", cs)
	}
	// 种子已落盘
	if got := LoadState(dir).Cert("c2").LastError; got != "old" {
		t.Fatal("种子未落盘")
	}
}

func TestLoadMigratesRuntimeStateFromLegacyConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(dir+".key", key, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"settings":{"adminUser":"admin","adminPassHash":"hash","totpLastCounter":77},"providers":[],"ddns":[],"certs":[{"id":"c1","name":"n","enabled":true,"domains":["a.com"],"providerId":"p1","email":"","caDirUrl":"","renewDays":30,"certFile":"certs/c1.crt","keyFile":"certs/c1.key","notAfter":"2030-06-01T00:00:00Z","lastError":"x"}],"sites":[],"forwards":[]}`)
	encrypted, err := encryptConfig(legacy, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), encrypted, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 运行状态已迁入状态库
	if got := cfg.State().TOTPCounter(); got != 77 {
		t.Fatalf("迁移后 TOTPCounter = %d, want 77", got)
	}
	cs := cfg.State().Cert("c1")
	if cs.NotAfter != "2030-06-01T00:00:00Z" || cs.CertFile != "certs/c1.crt" || cs.LastError != "x" {
		t.Fatalf("迁移后证书状态: %+v", cs)
	}
	// 加密配置已被剥离运行字段重写
	onDisk, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := decryptIfNeeded(onDisk, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("notAfter")) || bytes.Contains(plain, []byte("totpLastCounter")) || bytes.Contains(plain, []byte("certFile")) {
		t.Fatalf("运行字段仍残留在加密配置中: %s", plain)
	}
	// 重启后状态仍在
	cfg2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.State().TOTPCounter() != 77 || cfg2.State().Cert("c1").NotAfter == "" {
		t.Fatal("重启后运行状态丢失")
	}
}

func TestRestoreStripsAndSeedsRuntimeState(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	backup := []byte(`{"settings":{"adminUser":"admin","adminPassHash":"hash","totpLastCounter":9},"certs":[{"id":"c1","name":"n","domains":["a.com"],"providerId":"p1","notAfter":"2030-01-01T00:00:00Z"}]}`)
	if err := cfg.Restore(backup); err != nil {
		t.Fatal(err)
	}
	// 备份里的运行状态进入状态库，而不是加密配置
	if got := cfg.State().TOTPCounter(); got != 9 {
		t.Fatalf("Restore 后 TOTPCounter = %d, want 9", got)
	}
	if cs := cfg.State().Cert("c1"); cs.NotAfter != "2030-01-01T00:00:00Z" {
		t.Fatalf("Restore 后证书状态: %+v", cs)
	}
	plain, err := cfg.PlainJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("notAfter")) || bytes.Contains(plain, []byte("totpLastCounter")) {
		t.Fatalf("运行字段进入加密配置/备份导出: %s", plain)
	}
}
