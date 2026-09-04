package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// buildFakeRun 现场构造一个假 .run：脚本头 + payload 标记 + tar.gz（内含假 ELF 二进制）。
func buildFakeRun(t *testing.T, binContent []byte, binName string) string {
	t.Helper()

	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     binName,
		Typeflag: tar.TypeReg,
		Mode:     0o755,
		Size:     int64(len(binContent)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binContent); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	var buf bytes.Buffer
	buf.WriteString("#!/bin/sh\n# fake installer\nexit 0\n")
	buf.WriteString(payloadMarker + "\n")
	buf.Write(tgz.Bytes())

	path := filepath.Join(t.TempDir(), "fake.run")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractPayload(t *testing.T) {
	bin := append([]byte{0x7f, 'E', 'L', 'F'}, bytes.Repeat([]byte{0xAB}, 1024)...)
	runPath := buildFakeRun(t, bin, "files/andey-proxy")

	dir, err := extractPayload(runPath)
	if err != nil {
		t.Fatalf("extractPayload 失败: %v", err)
	}
	defer os.RemoveAll(dir)

	got, err := os.ReadFile(filepath.Join(dir, binaryName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bin) {
		t.Errorf("解出的二进制内容不一致：got %d bytes, want %d bytes", len(got), len(bin))
	}
	info, err := os.Stat(filepath.Join(dir, binaryName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("解出文件权限 = %o, want 755", info.Mode().Perm())
	}
}

func TestExtractPayloadNoMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.run")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractPayload(path); err == nil {
		t.Error("缺少 payload 标记时应报错")
	}
}

func TestExtractPayloadNotELF(t *testing.T) {
	runPath := buildFakeRun(t, []byte("not an elf binary at all"), "andey-proxy")
	if _, err := extractPayload(runPath); err == nil {
		t.Error("非 ELF 内容应报校验错误")
	}
}

func TestExtractPayloadNoBinary(t *testing.T) {
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, bytes.Repeat([]byte{0}, 64)...)
	runPath := buildFakeRun(t, elf, "other-name")
	if _, err := extractPayload(runPath); err == nil {
		t.Error("包内无 andey-proxy 时应报错")
	}
}
