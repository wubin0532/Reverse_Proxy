package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestConfigEncryptedAndTamperFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Lock()
	cfg.Providers = []DNSProviderConf{{ID: "p1", Type: "cloudflare", Key: "CANARY-TOKEN"}}
	cfg.Unlock()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("CANARY-TOKEN")) {
		t.Fatal("encrypted config leaked plaintext token")
	}
	if st, err := os.Stat(dir + ".key"); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions: %v %v", st, err)
	}
	raw[len(raw)-8] ^= 1
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("tampered ciphertext must fail closed")
	}
}

func TestPlaintextConfigMigratesWithoutBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"settings":{"adminUser":"admin","adminPort":16601}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if bytes.Contains(raw, []byte("adminPort")) {
		t.Fatal("plaintext config was not migrated")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json.bak")); !os.IsNotExist(err) {
		t.Fatal("plaintext backup must not remain")
	}
}

func TestMissingKeyFailsWithoutReplacingRecoveryState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	keyPath := dir + ".key"
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !os.IsNotExist(err) {
		t.Fatalf("missing encrypted-config key error = %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("missing key must not be replaced, stat error = %v", err)
	}
}

func TestEncryptedLegacyFieldsArePurged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(dir+".key", key, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"settings":{"adminUser":"admin","adminPassHash":"hash","webhook":{"key":"LEGACY-CANARY"}},"providers":[],"ddns":[],"certs":[],"sites":[],"forwards":[]}`)
	encrypted, err := encryptConfig(legacy, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), encrypted, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := decryptIfNeeded(onDisk, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("LEGACY-CANARY")) || bytes.Contains(plain, []byte("webhook")) {
		t.Fatalf("废弃通知凭据仍残留在配置中: %s", plain)
	}
}

func TestSaveFailureRollsBackMemory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := cfg.Settings.AdminUser
	cfg.Lock()
	cfg.Settings.AdminUser = "not-persisted"
	cfg.Unlock()
	cfg.filePath = filepath.Join(dir, "missing", "config.json")
	if err := cfg.Save(); err == nil {
		t.Fatal("expected save failure")
	}
	cfg.RLock()
	got := cfg.Settings.AdminUser
	cfg.RUnlock()
	if got != original {
		t.Fatalf("memory changed after failed save: %q", got)
	}
}

func TestConcurrentUpdatesDoNotLoseChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	const count = 24
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := cfg.Update(func(c *Config) error {
				c.Providers = append(c.Providers, DNSProviderConf{ID: strconv.Itoa(i), Type: "cloudflare", Key: "secret"})
				return nil
			}); err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Providers) != count {
		t.Fatalf("并发更新丢失: got %d want %d", len(reloaded.Providers), count)
	}
}

func TestLoadRejectsFilesystemRoot(t *testing.T) {
	if _, err := Load(string(os.PathSeparator)); err == nil {
		t.Fatal("filesystem root must not be accepted as a config directory")
	}
}
