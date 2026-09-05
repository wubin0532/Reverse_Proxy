// Package ids 提供全局统一的随机 ID 生成。
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

// New 生成 128 位 crypto/rand hex ID（32 个十六进制字符）。
// crypto/rand 失败意味着系统熵源故障，无法安全继续，直接 panic。
func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
