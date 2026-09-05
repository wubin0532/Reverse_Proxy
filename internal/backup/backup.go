// Package backup 实现配置备份文件的加密与解密。
// 备份文件是自描述 JSON：元信息 + scrypt KDF 参数 + AES-256-GCM 密文。
// 密钥由用户输入的备份口令派生，与设备绑定的 .key 文件无关，因此可跨设备迁移。
package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/scrypt"
)

const (
	// AppID 备份文件标识，导入时校验防止误传其他文件。
	AppID = "andey-proxy-backup"
	// FormatVersion 备份文件格式版本。
	FormatVersion = 1

	// aad 作为 GCM 附加认证数据，防止密文被移到其他用途的上下文。
	aad = "andey-proxy-backup-v1"

	// scrypt 参数：内存约 32 MiB，路由器级设备可承受。
	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
	keyLen  = 32
)

type kdfParams struct {
	Algo string `json:"algo"` // scrypt
	N    int    `json:"n"`
	R    int    `json:"r"`
	P    int    `json:"p"`
	Salt string `json:"salt"` // base64
}

type file struct {
	App        string    `json:"app"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exportedAt"`
	AppVersion string    `json:"appVersion,omitempty"`
	KDF        kdfParams `json:"kdf"`
	Cipher     string    `json:"cipher"` // AES-256-GCM
	Nonce      string    `json:"nonce"`  // base64
	Data       string    `json:"data"`   // base64 密文
}

// Encrypt 用备份口令加密配置明文，返回备份文件内容（JSON）。
func Encrypt(plain []byte, password, appVersion string, now time.Time) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	f := file{
		App:        AppID,
		Version:    FormatVersion,
		ExportedAt: now.UTC(),
		AppVersion: appVersion,
		KDF: kdfParams{
			Algo: "scrypt",
			N:    scryptN,
			R:    scryptR,
			P:    scryptP,
			Salt: base64.StdEncoding.EncodeToString(salt),
		},
		Cipher: "AES-256-GCM",
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Data:   base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, []byte(aad))),
	}
	return json.MarshalIndent(f, "", "  ")
}

// Decrypt 解析备份文件并用备份口令解密，返回配置明文 JSON。
func Decrypt(data []byte, password string) ([]byte, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, errors.New("备份文件不是有效的 JSON")
	}
	if f.App != AppID || f.Version != FormatVersion {
		return nil, errors.New("不是有效的 andey-proxy 备份文件")
	}
	if f.Cipher != "AES-256-GCM" || f.KDF.Algo != "scrypt" {
		return nil, errors.New("备份文件使用了不支持的加密算法")
	}
	// Format v1 only emits this fixed KDF. Check before allocating memory or
	// authenticating the ciphertext: these parameters are untrusted input.
	if f.KDF.N != scryptN || f.KDF.R != scryptR || f.KDF.P != scryptP {
		return nil, errors.New("备份文件的 KDF 参数无效")
	}
	salt, err := base64.StdEncoding.DecodeString(f.KDF.Salt)
	if err != nil || len(salt) != 16 {
		return nil, errors.New("备份文件的盐值无效")
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil || len(nonce) != 12 {
		return nil, errors.New("备份文件的 nonce 无效")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil || len(ciphertext) < 16 {
		return nil, errors.New("备份文件的密文无效")
	}
	key, err := scrypt.Key([]byte(password), salt, f.KDF.N, f.KDF.R, f.KDF.P, keyLen)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, fmt.Errorf("备份解密失败：口令错误或文件已被篡改")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
