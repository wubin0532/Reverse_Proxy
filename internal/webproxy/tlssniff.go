package webproxy

import (
	"bufio"
	"crypto/tls"
	"net"
	"time"
)

// tlsSniffListener 在同一端口分流 TLS 与明文连接：
// 窥视首字节，0x16（TLS handshake 记录类型）走 TLS，其余按明文 HTTP 处理。
// 用于站点开启"强制 HTTPS"后，同一监听端口同时服务 HTTPS 与明文 301 跳转。
type tlsSniffListener struct {
	net.Listener
	tlsConfig *tls.Config
}

// Accept 返回的下一条连接按首字节分流。窥视设置读超时（与 ReadHeaderTimeout 同级），
// 防止对端连接后不发数据阻塞整个 Accept 循环；窥视失败（如对端秒断）直接丢弃继续。
func (l *tlsSniffListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		br := bufio.NewReader(conn)
		first, err := br.Peek(1)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			_ = conn.Close()
			continue
		}
		sc := &sniffConn{Conn: conn, br: br}
		if first[0] == 0x16 {
			return tls.Server(sc, l.tlsConfig), nil
		}
		return sc, nil
	}
}

// sniffConn 把窥视缓冲接回读取流，保证已 peek 的字节不丢失。
type sniffConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *sniffConn) Read(p []byte) (int, error) { return c.br.Read(p) }
