package webproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

// TestReverseBackendFailover 一死一活两个后端：连接类失败达阈值后进入冷却期，
// 请求（含失败后重试）全部落到活节点；冷却期过后恢复轮询。
func TestReverseBackendFailover(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "alive")
	}))
	defer alive.Close()
	// 监听后立刻关闭，得到必定连接被拒绝的死后端地址。
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadURL := "http://" + l.Addr().String()
	_ = l.Close()

	h, err := newReverseHandler(config.SubRule{ID: "fo", Name: "fo", Backends: []string{deadURL, alive.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	rh := h.(*reverseHandler)
	for i := 0; i < 6; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://public.example/", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rh.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "alive" {
			t.Fatalf("request %d = %d %q，请求应全部落到活后端", i, rec.Code, rec.Body.String())
		}
	}
	// 死后端已进入冷却期且失败计数未被清零
	if rh.proxies[0].failures.Load() < backendFailThreshold {
		t.Fatalf("死后端失败计数 = %d，应 >= %d", rh.proxies[0].failures.Load(), backendFailThreshold)
	}
	if rh.proxies[0].coolUntil.Load() <= time.Now().UnixNano() {
		t.Fatal("死后端应处于冷却期")
	}
	if rh.proxies[1].failures.Load() != 0 {
		t.Fatalf("活后端失败计数 = %d，成功请求应清零", rh.proxies[1].failures.Load())
	}
	// 冷却期过后恢复轮询：死后端重新参与选择
	rh.proxies[0].coolUntil.Store(time.Now().Add(-time.Second).UnixNano())
	if got := rh.pick(nil); got != rh.proxies[0] {
		t.Fatal("冷却期过后死后端应恢复参与轮询")
	}
}
