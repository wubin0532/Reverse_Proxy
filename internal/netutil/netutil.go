// Package netutil 提供网络相关的公共小工具。
package netutil

import (
	"net"
	"strconv"
	"strings"
)

// ListenPort 从监听地址（":8080"、"127.0.0.1:8080" 或裸 "8080"）解析端口号。
func ListenPort(listen string) (int, error) {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		p = strings.TrimPrefix(listen, ":")
	}
	return strconv.Atoi(p)
}
