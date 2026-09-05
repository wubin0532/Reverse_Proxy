package logcenter

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteRedactsAndExtractsStructuredFields(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("2026/09/05 10:00:00 [security] 修改站点，ID: site-1，客户端 IP: 192.0.2.4 Authorization: Bearer CANARY\n"))
	entries, _ := c.Query(Query{Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	got := entries[0]
	if got.Source != "security" || got.EntityID != "site-1" || got.ClientIP != "192.0.2.4" {
		t.Fatalf("structured entry = %+v", got)
	}
	if strings.Contains(got.Message, "CANARY") {
		t.Fatal("Write leaked credential")
	}
}

func TestLoadRedactsLegacyEntries(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Entry{Message: "access_token=LEGACY-CANARY", Source: "system", Level: "info"})
	if err := os.WriteFile(filepath.Join(logDir, "andey-proxy.log"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	entries, _ := c.Query(Query{Limit: 10})
	if len(entries) != 1 || strings.Contains(entries[0].Message, "LEGACY-CANARY") {
		t.Fatalf("legacy entry was not redacted: %+v", entries)
	}
}

func TestRedactionAndRotationLimit(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	canaries := []string{
		"Authorization: Bearer abc.def.ghi",
		"request ?access_token=CANARY&safe=1",
		`payload {"apiKey":"JSON-CANARY","name":"ok"}`,
		"backend https://user:PASS@example.test/path",
		"Cookie: session=COOKIE-CANARY; other=value",
	}
	for _, input := range canaries {
		got := Redact(input)
		for _, secret := range []string{"abc.def.ghi", "CANARY", "JSON-CANARY", "PASS", "COOKIE-CANARY"} {
			if strings.Contains(got, secret) {
				t.Fatalf("redaction leaked %q: %s", secret, got)
			}
		}
	}
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	for i := 0; i < 100; i++ {
		c.writeBatch(chunk)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, entry := range entries {
		st, _ := entry.Info()
		total += st.Size()
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("log permissions = %o", st.Mode().Perm())
		}
	}
	if total > 5*(1<<20) {
		t.Fatalf("rotated logs use %d bytes, expected <= 5 MiB", total)
	}
}

func TestClearAfterCloseDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Clear() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Clear after Close should return an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Clear after Close blocked")
	}
}
