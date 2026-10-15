package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// buildRun 组装一个 .run：脚本头 + payload 标记 + gzip(tar)。
func buildRun(t *testing.T, files map[string][]byte, order []string) string {
	t.Helper()
	var payload bytes.Buffer
	gz := gzip.NewWriter(&payload)
	tw := tar.NewWriter(gz)
	names := order
	if len(names) == 0 {
		for n := range files {
			names = append(names, n)
		}
	}
	for _, n := range names {
		data := files[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.run")
	out := append([]byte("#!/bin/sh\nVERSION=1.0.0\n"+payloadMarker+"\n"), payload.Bytes()...)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// 更新包的解析与拒绝路径是安全关键路径：每一种畸形都必须带明确原因失败。
func TestInspectSignedRunRejectsMalformedPackages(t *testing.T) {
	dir := t.TempDir()

	validSig := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 64))
	manifest := func(size int, digest string) []byte {
		return []byte(`{"version":"1.0.0","goos":"linux","goarch":"amd64","size":` + strconv.Itoa(size) + `,"sha256":"` + digest + `"}`)
	}
	blob := []byte("not-an-elf-binary")
	digest := hex.EncodeToString(func() []byte { s := sha256.Sum256(blob); return s[:] }())

	good := map[string][]byte{
		"manifest.json": manifest(len(blob), digest),
		"manifest.sig":  []byte(validSig),
		binaryName:      blob,
	}

	cases := []struct {
		name    string
		files   map[string][]byte
		order   []string
		header  string
		wantSub string
	}{
		{name: "缺少 payload 标记", files: good, header: "#!/bin/sh\n", wantSub: "payload 标记"},
		{name: "payload 不是 gzip", files: good, header: "#!/bin/sh\n" + payloadMarker + "\n", wantSub: ""},
		{name: "缺少清单", files: map[string][]byte{"manifest.sig": []byte(validSig), binaryName: blob}, wantSub: "缺少"},
		{name: "缺少签名", files: map[string][]byte{"manifest.json": good["manifest.json"], binaryName: blob}, wantSub: "缺少"},
		{name: "缺少二进制", files: map[string][]byte{"manifest.json": good["manifest.json"], "manifest.sig": []byte(validSig)}, wantSub: "缺少"},
		{name: "重复清单", files: map[string][]byte{"manifest.json": good["manifest.json"], "manifest.sig": []byte(validSig), binaryName: blob}, order: []string{"manifest.json", "manifest.json", "manifest.sig", binaryName}, wantSub: "重复清单"},
		{name: "重复签名", files: map[string][]byte{"manifest.json": good["manifest.json"], "manifest.sig": []byte(validSig), binaryName: blob}, order: []string{"manifest.json", "manifest.sig", "manifest.sig", binaryName}, wantSub: "重复签名"},
		{name: "重复二进制", files: map[string][]byte{"manifest.json": good["manifest.json"], "manifest.sig": []byte(validSig), binaryName: blob}, order: []string{"manifest.json", "manifest.sig", binaryName, binaryName}, wantSub: "重复二进制"},
		{name: "签名无效", files: map[string][]byte{"manifest.json": good["manifest.json"], "manifest.sig": []byte("not-base64!!"), binaryName: blob}, wantSub: "签名验证失败"},
		{name: "签名不匹配", files: good, wantSub: "签名验证失败"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			if tc.header != "" {
				// 自定义头部（缺少或畸形）的包
				var payload bytes.Buffer
				gzWriter := gzip.NewWriter(&payload)
				tw := tar.NewWriter(gzWriter)
				for n, data := range tc.files {
					_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
					_, _ = tw.Write(data)
				}
				_ = tw.Close()
				_ = gzWriter.Close()
				path = filepath.Join(t.TempDir(), "custom.run")
				body := append([]byte(tc.header), payload.Bytes()...)
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				path = buildRun(t, tc.files, tc.order)
			}
			_, _, err := inspectSignedRun(path, dir)
			if tc.wantSub == "" {
				if err == nil {
					t.Fatal("期望解析失败")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("期望错误包含 %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestElfMatchesArchitecture(t *testing.T) {
	cases := []struct {
		machine elf.Machine
		data    elf.Data
		arch    string
		want    bool
	}{
		{elf.EM_X86_64, elf.ELFDATA2LSB, "amd64", true},
		{elf.EM_X86_64, elf.ELFDATA2MSB, "amd64", false},
		{elf.EM_AARCH64, elf.ELFDATA2LSB, "arm64", true},
		{elf.EM_ARM, elf.ELFDATA2LSB, "arm", true},
		{elf.EM_MIPS, elf.ELFDATA2MSB, "mips", true},
		{elf.EM_MIPS, elf.ELFDATA2LSB, "mipsle", true},
		{elf.EM_MIPS, elf.ELFDATA2LSB, "mips", false},
		{elf.EM_X86_64, elf.ELFDATA2LSB, "arm64", false},
		{elf.EM_ARM, elf.ELFDATA2LSB, "unknown-arch", false},
	}
	for _, tc := range cases {
		if got := elfMatches(tc.machine, tc.data, tc.arch); got != tc.want {
			t.Errorf("elfMatches(%v,%v,%q)=%v want %v", tc.machine, tc.data, tc.arch, got, tc.want)
		}
	}
}

func TestManagerUnknownOperations(t *testing.T) {
	m := NewManager("1.0.0", t.TempDir())
	if m.Version() != "1.0.0" {
		t.Fatalf("version=%q", m.Version())
	}
	if m.Cancel("missing") {
		t.Fatal("取消未知任务应返回 false")
	}
	if err := m.Install("missing", false); err == nil {
		t.Fatal("安装未知任务应报错")
	}
	if st := m.Status(); st.Error != "" || st.Inspection != nil {
		t.Fatalf("新建管理器不应带有错误或待安装包: %+v", st)
	}
}
