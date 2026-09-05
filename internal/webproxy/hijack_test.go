package webproxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func countHijacked(ss *siteServer) int {
	n := 0
	ss.hijacked.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// TestHijackedConnTracked 验证 statusWriter.Hijack 成功后将连接登记到 siteServer，
// closeHijackedConnections 统一关闭并自动移除登记。
func TestHijackedConnTracked(t *testing.T) {
	ss := &siteServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK, ss: ss}
		conn, _, err := sw.Hijack()
		if err != nil {
			t.Errorf("Hijack 失败: %v", err)
			return
		}
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
		// 保持连接直到被站点关闭（模拟 WebSocket 长连接）
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "ok" {
		t.Fatalf("响应体异常: %q, %v", body, err)
	}

	if got := countHijacked(ss); got != 1 {
		t.Fatalf("Hijack 后应登记 1 条连接, got %d", got)
	}

	ss.closeHijackedConnections()
	if got := countHijacked(ss); got != 0 {
		t.Fatalf("关闭后登记表应为空, got %d", got)
	}
}
