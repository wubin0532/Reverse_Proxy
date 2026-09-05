package backup

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte(`{"settings":{"adminUser":"admin"},"sites":[{"id":"s1"}]}`)
	data, err := Encrypt(plain, "backup-pass-123", "0.2.0", time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, plain) {
		t.Fatal("备份文件不应包含明文配置")
	}
	got, err := Decrypt(data, "backup-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("解密结果与明文不一致: %s", got)
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	data, err := Encrypt([]byte(`{"settings":{}}`), "backup-pass-123", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(data, "backup-pass-456"); err == nil {
		t.Fatal("错误口令应解密失败")
	}
}

func TestDecryptTampered(t *testing.T) {
	data, err := Encrypt([]byte(`{"settings":{}}`), "backup-pass-123", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// 篡改密文的一个字节（base64 字符串中部）
	i := bytes.LastIndexByte(data, '"')
	data[i-3] ^= 1
	if _, err := Decrypt(data, "backup-pass-123"); err == nil {
		t.Fatal("篡改后的备份应解密失败")
	}
}

func TestDecryptRejectsForeignJSON(t *testing.T) {
	for _, blob := range []string{
		`{"foo":"bar"}`,
		`not json`,
		`{"app":"andey-proxy-backup","version":99}`,
	} {
		if _, err := Decrypt([]byte(blob), "x"); err == nil {
			t.Fatalf("应拒绝非备份文件: %s", blob)
		}
	}
}

func TestDecryptRejectsCrazyKDF(t *testing.T) {
	data, err := Encrypt([]byte(`{}`), "backup-pass-123", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// 把 N 改成巨大值，须在派生密钥前被拒绝而不是耗尽内存
	crazy := strings.Replace(string(data), `"n": 32768`, `"n": 1073741824`, 1)
	if crazy == string(data) {
		t.Fatal("测试前提失败：未找到 N 参数")
	}
	if _, err := Decrypt([]byte(crazy), "backup-pass-123"); err == nil {
		t.Fatal("应拒绝超出上限的 scrypt N")
	}
}
