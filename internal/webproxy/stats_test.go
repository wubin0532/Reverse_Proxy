package webproxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"andey-proxy/internal/config"
)

// TestSiteStatsCounting 站点级流量统计：请求数、入/出字节、状态码分桶。
func TestSiteStatsCounting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "boom!!")
			return
		}
		fmt.Fprint(w, "hello")
	}))
	defer backend.Close()

	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{
			ID: "r1", Name: "反代", Type: "reverse", Enabled: true,
			FrontendPath: "/", Backends: []string{backend.URL},
		}},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")
	client := httpClient()

	// 200，带 11 字节请求体
	reqBody := "hello-body!" // 11 字节
	req, err := http.NewRequest("POST", "http://"+addr+"/ok", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应为 200, got %d", resp.StatusCode)
	}

	// 500
	code, _, body500 := mustGet(t, client, "http://"+addr+"/fail", nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("应为 500, got %d", code)
	}

	// 404（无匹配子规则的场景用单独站点验证，这里后端无 /fail 以外路径仍 200；
	// 直接构造一个不匹配规则的路径）
	// 站点 r1 前缀为 "/" 会匹配一切，改发站点级 404：见下方 removeRule 场景。
	// 此处用一个禁用规则站点验证 404 桶。
	addSite(cfg, config.Site{
		ID: "s2", Name: "空站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{
			ID: "r2", Name: "禁用", Type: "reverse", Enabled: false,
			FrontendPath: "/", Backends: []string{backend.URL},
		}},
	})
	svc.Reload()
	addr2 := svc.ListenAddr("s2")
	code, _, body404 := mustGet(t, client, "http://"+addr2+"/x", nil)
	if code != http.StatusNotFound {
		t.Fatalf("应为 404, got %d", code)
	}

	st := svc.AllSiteStats()["s1"]
	if st.Requests != 2 || st.Status2xx != 1 || st.Status5xx != 1 || st.Status4xx != 0 {
		t.Fatalf("s1 统计错误: %+v", st)
	}
	if st.BytesIn != int64(len(reqBody)) {
		t.Fatalf("入字节应为 %d, got %d", len(reqBody), st.BytesIn)
	}
	wantOut := int64(len("hello") + len(body500))
	if st.BytesOut != wantOut {
		t.Fatalf("出字节应为 %d, got %d", wantOut, st.BytesOut)
	}

	st2 := svc.AllSiteStats()["s2"]
	if st2.Requests != 1 || st2.Status4xx != 1 || st2.BytesOut != int64(len(body404)) {
		t.Fatalf("s2 统计错误: %+v", st2)
	}
}

// TestSiteStatsSurviveReload 热重载（非监听配置变化）保留统计；删除站点时清理。
func TestSiteStatsSurviveReload(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{
			ID: "r1", Name: "反代", Type: "reverse", Enabled: true,
			FrontendPath: "/", Backends: []string{backend.URL},
		}},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	// 并发请求，配合 -race 验证统计并发正确性
	var wg sync.WaitGroup
	var failed atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := httpClient()
			for j := 0; j < 5; j++ {
				resp, err := client.Get("http://" + addr + "/")
				if err != nil {
					failed.Add(1)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					failed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.Fatalf("有 %d 个并发请求失败", failed.Load())
	}

	if st := svc.AllSiteStats()["s1"]; st.Requests != 40 || st.Status2xx != 40 {
		t.Fatalf("并发统计错误: %+v", st)
	}

	// 热更新规则名（非监听配置变化，重建 handler 缓存）→ 统计保留
	cfg.Lock()
	cfg.Sites[0].Rules[0].Name = "反代-改"
	cfg.Unlock()
	if err := svc.Reload(); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if st := svc.AllSiteStats()["s1"]; st.Requests != 40 {
		t.Fatalf("热重载后统计应保留, got %+v", st)
	}

	// 删除站点 → 统计清理
	removeSite(cfg, "s1")
	if err := svc.Reload(); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if _, ok := svc.AllSiteStats()["s1"]; ok {
		t.Fatal("站点删除后统计应被清理")
	}
}
