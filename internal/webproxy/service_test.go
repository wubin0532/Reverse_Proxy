package webproxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/forward"
)

// --- 测试辅助 ---

func newTestRingLog() *forward.RingLog { return forward.NewRingLog(100) }

// newRequest 构造测试请求；host 用 URL 中的主机。
func newRequest(method, url, ua string) *http.Request {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		panic(err)
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	return req
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// newTestService 创建配置（临时目录）与 Service，测试结束自动 Stop。
func newTestService(t *testing.T) (*config.Config, *Service) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	svc := NewService(cfg, nil)
	t.Cleanup(svc.Stop)
	return cfg, svc
}

// addSite 向配置追加站点。
func addSite(cfg *config.Config, site config.Site) {
	cfg.Lock()
	cfg.Sites = append(cfg.Sites, site)
	cfg.Unlock()
}

// removeSite 从配置删除站点。
func removeSite(cfg *config.Config, id string) {
	cfg.Lock()
	for i := range cfg.Sites {
		if cfg.Sites[i].ID == id {
			cfg.Sites = append(cfg.Sites[:i], cfg.Sites[i+1:]...)
			break
		}
	}
	cfg.Unlock()
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

// mustGet 发起 GET 并读完整响应。
func mustGet(t *testing.T, client *http.Client, url string, mutate func(*http.Request)) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if mutate != nil {
		mutate(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应失败: %v", err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

// --- 端到端测试 ---

// TestReverseEndToEnd 反代透传：路径拼接、Header 改写、PreserveHost=false。
func TestReverseEndToEnd(t *testing.T) {
	var gotHost, gotPath, gotQuery, gotCustom, gotXFF string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotCustom = r.Header.Get("X-Custom")
		gotXFF = r.Header.Get("X-Forwarded-For")
		fmt.Fprint(w, "backend-ok")
	}))
	defer backend.Close()
	backendHost := strings.TrimPrefix(backend.URL, "http://")

	cfg, svc := newTestService(t)
	site := config.Site{
		ID: "s1", Name: "站点1", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{
			ID: "r1", Name: "反代", Type: "reverse", Enabled: true,
			FrontendPath: "/", Backends: []string{backend.URL + "/base"},
			Headers: map[string]string{"X-Custom": "yes"},
		}},
	}
	addSite(cfg, site)
	svc.Start()

	addr := svc.ListenAddr("s1")
	if addr == "" {
		t.Fatal("站点未在监听")
	}
	code, _, body := mustGet(t, httpClient(), "http://"+addr+"/foo/bar?x=1", nil)
	if code != http.StatusOK || body != "backend-ok" {
		t.Fatalf("反代透传失败: code=%d body=%q", code, body)
	}
	if gotPath != "/base/foo/bar" {
		t.Fatalf("后端路径前缀拼接错误: %q", gotPath)
	}
	if gotQuery != "x=1" {
		t.Fatalf("查询串未透传: %q", gotQuery)
	}
	if gotHost != backendHost {
		t.Fatalf("PreserveHost=false 时 Host 应为后端 host %q, got %q", backendHost, gotHost)
	}
	if gotCustom != "yes" {
		t.Fatalf("附加 Header 未写入: %q", gotCustom)
	}
	if gotXFF == "" {
		t.Fatal("X-Forwarded-For 应由 ReverseProxy 自动追加")
	}

	// 访问日志应有记录
	logs := svc.Logs("s1")
	if len(logs) == 0 || !strings.Contains(logs[len(logs)-1], "规则[反代] 200") {
		t.Fatalf("访问日志缺失或内容不对: %v", logs)
	}
}

// TestReversePreserveHost PreserveHost=true 时透传原始 Host。
func TestReversePreserveHost(t *testing.T) {
	var gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{
			ID: "r1", Name: "反代", Type: "reverse", Enabled: true,
			FrontendPath: "/", Backends: []string{backend.URL}, PreserveHost: true,
		}},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	code, _, _ := mustGet(t, httpClient(), "http://"+addr+"/", func(r *http.Request) {
		r.Host = "original.example.com"
	})
	if code != http.StatusOK {
		t.Fatalf("反代失败: %d", code)
	}
	if gotHost != "original.example.com" {
		t.Fatalf("PreserveHost=true 时应透传原始 Host, got %q", gotHost)
	}
}

// TestReverseRoundRobin 多后端轮询。
func TestReverseRoundRobin(t *testing.T) {
	mk := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, name)
		}))
	}
	b1, b2 := mk("b1"), mk("b2")
	defer b1.Close()
	defer b2.Close()

	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{
			ID: "r1", Name: "反代", Type: "reverse", Enabled: true,
			FrontendPath: "/", Backends: []string{b1.URL, b2.URL},
		}},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		_, _, body := mustGet(t, httpClient(), "http://"+addr+"/", nil)
		seen[body]++
	}
	if seen["b1"] != 2 || seen["b2"] != 2 {
		t.Fatalf("轮询不均: %v", seen)
	}
}

// TestReverseBackendDown 后端不可达返回 502 并记日志。
func TestReverseBackendDown(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{
			ID: "r1", Name: "反代", Type: "reverse", Enabled: true,
			FrontendPath: "/", Backends: []string{"http://127.0.0.1:1"},
		}},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	code, _, _ := mustGet(t, httpClient(), "http://"+addr+"/", nil)
	if code != http.StatusBadGateway {
		t.Fatalf("后端不可达应为 502, got %d", code)
	}
}

// TestSiteDispatchAndNotFound 站点内按规则类型分发 + 无匹配 404 页。
func TestSiteDispatchAndNotFound(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{
			{ID: "r1", Name: "跳转", Type: "redirect", Enabled: true, FrontendPath: "/go", RedirectURL: "https://example.com{path}", RedirectCode: 301},
		},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	// 命中 redirect（不跟随跳转）
	noFollow := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	code, header, _ := mustGet(t, noFollow, "http://"+addr+"/go/deep", nil)
	if code != http.StatusMovedPermanently {
		t.Fatalf("应为 301, got %d", code)
	}
	if loc := header.Get("Location"); loc != "https://example.com/go/deep" {
		t.Fatalf("redirect {path} 替换错误: %q", loc)
	}

	// 无匹配 → 404 提示页
	code, _, body := mustGet(t, httpClient(), "http://"+addr+"/nothing", nil)
	if code != http.StatusNotFound || !strings.Contains(body, "没有匹配") {
		t.Fatalf("无匹配应为 404 提示页, code=%d body=%q", code, body)
	}
}

// TestFileServer 文件服务：正常读取 + 目录穿越被拒。
func TestFileServer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hello-andey-proxy")

	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{
			{ID: "r1", Name: "文件", Type: "fileserver", Enabled: true, FrontendPath: "/files", RootDir: dir},
		},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	code, _, body := mustGet(t, httpClient(), "http://"+addr+"/files/hello.txt", nil)
	if code != http.StatusOK || body != "hello-andey-proxy" {
		t.Fatalf("文件服务失败: code=%d body=%q", code, body)
	}

	// 目录穿越：客户端层会规范化 ../，直接发原始路径验证服务端兜底
	req, _ := http.NewRequest("GET", "http://"+addr+"/files/../webproxy_test.go", nil)
	req.URL.Opaque = "/files/../webproxy_test.go"
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("目录穿越不应成功")
	}
}

// TestGuardEndToEnd 站点级别 IP/UA/BasicAuth 拦截。
func TestGuardEndToEnd(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{
			{ID: "r1", Name: "IP白名单", Type: "reverse", Enabled: true, FrontendPath: "/ip",
				Backends: []string{backend.URL}, IPListMode: "whitelist", IPList: []string{"10.0.0.1"}},
			{ID: "r2", Name: "UA黑名单", Type: "reverse", Enabled: true, FrontendPath: "/ua",
				Backends: []string{backend.URL}, UAListMode: "blacklist", UAList: []string{"curl"}},
			{ID: "r3", Name: "认证", Type: "reverse", Enabled: true, FrontendPath: "/auth",
				Backends: []string{backend.URL}, BasicAuth: true, AuthUser: "a", AuthPass: "b"},
		},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")
	client := httpClient()

	// IP 白名单：XFF 命中放行
	code, _, _ := mustGet(t, client, "http://"+addr+"/ip", func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "10.0.0.1")
	})
	if code != http.StatusOK {
		t.Fatalf("IP 白名单应放行, got %d", code)
	}
	// 未命中（RemoteAddr 127.0.0.1）
	code, _, _ = mustGet(t, client, "http://"+addr+"/ip", nil)
	if code != http.StatusForbidden {
		t.Fatalf("IP 白名单未命中应为 403, got %d", code)
	}
	// UA 黑名单
	code, _, _ = mustGet(t, client, "http://"+addr+"/ua", func(r *http.Request) {
		r.Header.Set("User-Agent", "curl/8.0")
	})
	if code != http.StatusForbidden {
		t.Fatalf("UA 黑名单应为 403, got %d", code)
	}
	// BasicAuth
	code, header, _ := mustGet(t, client, "http://"+addr+"/auth", nil)
	if code != http.StatusUnauthorized || header.Get("WWW-Authenticate") == "" {
		t.Fatalf("BasicAuth 应为 401 + WWW-Authenticate, got %d", code)
	}
	code, _, body := mustGet(t, client, "http://"+addr+"/auth", func(r *http.Request) {
		r.SetBasicAuth("a", "b")
	})
	if code != http.StatusOK || body != "ok" {
		t.Fatalf("BasicAuth 通过应放行, got %d", code)
	}
}

// TestTLSSelfSignedFallback 无证书时自签兜底，TLS 站点可握手。
func TestTLSSelfSignedFallback(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "TLS站点", Enabled: true, Listen: "127.0.0.1:0", TLS: true, // CertID 空 → 自签
		Rules: []config.SubRule{
			{ID: "r1", Name: "跳转", Type: "redirect", Enabled: true, FrontendPath: "/", RedirectURL: "https://example.com/"},
		},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	code, _, _ := mustGet(t, client, "https://"+addr+"/", nil)
	if code != http.StatusFound {
		t.Fatalf("TLS 站点握手后应正常分发, got %d", code)
	}
}

// TestReloadAddRemoveSite Reload 增量增删站点。
func TestReloadAddRemoveSite(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "a", Name: "A", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{ID: "r", Name: "跳转", Type: "redirect", Enabled: true, RedirectURL: "https://a.example.com/"}},
	})
	svc.Start()
	addrA := svc.ListenAddr("a")
	if addrA == "" {
		t.Fatal("站点 A 未监听")
	}
	assertReachable(t, addrA, true)

	// 新增站点 B
	addSite(cfg, config.Site{
		ID: "b", Name: "B", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{{ID: "r", Name: "跳转", Type: "redirect", Enabled: true, RedirectURL: "https://b.example.com/"}},
	})
	svc.Reload()
	addrB := svc.ListenAddr("b")
	if addrB == "" {
		t.Fatal("Reload 后站点 B 未监听")
	}
	assertReachable(t, addrB, true)
	// A 应未受影响（监听地址不变）
	if svc.ListenAddr("a") != addrA {
		t.Fatal("Reload 不应重启未变化的站点 A")
	}

	// 删除 A
	removeSite(cfg, "a")
	svc.Reload()
	assertReachable(t, addrA, false)
	assertReachable(t, addrB, true)

	// 禁用 B
	cfg.Lock()
	for i := range cfg.Sites {
		if cfg.Sites[i].ID == "b" {
			cfg.Sites[i].Enabled = false
		}
	}
	cfg.Unlock()
	svc.Reload()
	if status, _ := svc.SiteStatus("b"); status != "stopped" {
		t.Fatalf("禁用后状态应为 stopped, got %s", status)
	}
	assertReachable(t, addrB, false)
}

// TestAccessLogContent 访问日志字段完整性。
func TestAccessLogContent(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: "127.0.0.1:0",
		Rules: []config.SubRule{
			{ID: "r1", Name: "跳转", Type: "redirect", Enabled: true, RedirectURL: "https://example.com/"},
		},
	})
	svc.Start()
	addr := svc.ListenAddr("s1")

	noFollow := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	mustGet(t, noFollow, "http://"+addr+"/path?q=1", func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "9.9.9.9")
	})

	logs := svc.Logs("s1")
	if len(logs) != 1 {
		t.Fatalf("应有 1 条日志, got %v", logs)
	}
	entry := logs[0]
	for _, want := range []string{"9.9.9.9", "GET", "/path?q=1", "规则[跳转]", "302"} {
		if !strings.Contains(entry, want) {
			t.Errorf("日志 %q 缺少 %q", entry, want)
		}
	}
	// 不存在站点的日志
	if l := svc.Logs("nope"); len(l) != 0 {
		t.Fatalf("未知站点应返回空日志, got %v", l)
	}
}

func assertReachable(t *testing.T, addr string, want bool) {
	t.Helper()
	conn, err := (&net.Dialer{Timeout: time.Second}).Dial("tcp", addr)
	if want && err != nil {
		t.Fatalf("地址 %s 应可达: %v", addr, err)
	}
	if !want && err == nil {
		conn.Close()
		t.Fatalf("地址 %s 应不可达", addr)
	}
	if err == nil {
		conn.Close()
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}
