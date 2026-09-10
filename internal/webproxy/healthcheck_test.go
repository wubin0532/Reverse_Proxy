package webproxy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHealthCheckConfValidate(t *testing.T) {
	ok := HealthCheckConf{Enabled: true, Type: "http", Path: "/healthz", IntervalSeconds: 10, TimeoutSeconds: 3, Rise: 2, Fall: 3}
	if err := ok.validate(); err != nil {
		t.Fatalf("合法配置应通过: %v", err)
	}
	// 零值填默认
	def := HealthCheckConf{}
	if err := def.validate(); err != nil {
		t.Fatalf("零值配置应通过: %v", err)
	}
	if def.Type != "tcp" || def.Path != "/" || def.IntervalSeconds != 10 || def.TimeoutSeconds != 3 || def.Rise != 1 || def.Fall != 2 {
		t.Fatalf("默认值错误: %+v", def)
	}
	bad := []HealthCheckConf{
		{Enabled: true, Type: "udp"},
		{Enabled: true, Type: "http", Path: "healthz"},
		{Enabled: true, Type: "http", Path: "/a b"},
		{Enabled: true, IntervalSeconds: 1},
		{Enabled: true, IntervalSeconds: 301},
		{Enabled: true, TimeoutSeconds: 61},
		{Enabled: true, IntervalSeconds: 5, TimeoutSeconds: 10},
		{Enabled: true, Rise: 11},
		{Enabled: true, Fall: 11},
	}
	for i, c := range bad {
		if err := c.validate(); err == nil {
			t.Fatalf("非法配置 %d 应被拒绝: %+v", i, c)
		}
	}
	// tcp 类型忽略 path
	c := HealthCheckConf{Enabled: true, Type: "tcp", Path: "/ignored"}
	if err := c.validate(); err != nil || c.Path != "/" {
		t.Fatalf("tcp 类型 path 应归一: %+v, %v", c, err)
	}
}

func TestHealthStorePersistence(t *testing.T) {
	dir := t.TempDir()
	st := newHealthStore(dir)
	if got := st.confFor("r1"); got.Enabled {
		t.Fatalf("未知规则应返回零值配置, got %+v", got)
	}
	conf := HealthCheckConf{Enabled: true, Type: "http", Path: "/h", IntervalSeconds: 5, TimeoutSeconds: 2, Rise: 2, Fall: 3}
	if err := st.set("r1", conf); err != nil {
		t.Fatal(err)
	}
	reloaded := newHealthStore(dir)
	if got := reloaded.confFor("r1"); got != conf {
		t.Fatalf("重载后配置不一致: want %+v, got %+v", conf, got)
	}
	if err := reloaded.prune(map[string]bool{"r2": true}); err != nil {
		t.Fatal(err)
	}
	if got := newHealthStore(dir).confFor("r1"); got.Enabled {
		t.Fatal("prune 应清理已删除规则的配置并落盘")
	}
	if err := reloaded.delete("r1"); err != nil {
		t.Fatal(err)
	}
}

// TestHealthProberTCP tcp 探测：死后端被摘除出轮询，恢复监听后重新上线。
func TestHealthProberTCP(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "alive")
	}))
	defer alive.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := l.Addr().String()
	_ = l.Close()

	h, err := newReverseHandler(config.SubRule{ID: "hc", Name: "hc", Backends: []string{"http://" + deadAddr, alive.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	rh := h.(*reverseHandler)
	rule := config.SubRule{ID: "hc", Name: "hc"}
	rh.configureHealth(HealthCheckConf{Enabled: true, Type: "tcp", IntervalSeconds: 1, TimeoutSeconds: 1, Fall: 1, Rise: 1}, rule)
	defer rh.shutdown()
	if !rh.activeCheck.Load() {
		t.Fatal("主动检查应已启用")
	}

	waitFor(t, "死后端被摘除", func() bool { return !rh.proxies[0].up.Load() })
	if got := rh.pick(nil); got != rh.proxies[1] {
		t.Fatal("摘除后请求应全部落到活后端")
	}
	health := rh.backendHealth()
	if len(health) != 2 || health[0].Up || !health[0].ActiveCheck || health[0].LastError == "" || health[0].LastCheck == 0 {
		t.Fatalf("死后端健康快照错误: %+v", health)
	}
	if !health[1].Up || health[1].LatencyMs < 0 {
		t.Fatalf("活后端健康快照错误: %+v", health)
	}

	// 恢复：原地址重新监听，tcp 探测成功后重新上线
	rel, err := net.Listen("tcp", deadAddr)
	if err != nil {
		t.Fatalf("重新监听失败: %v", err)
	}
	defer rel.Close()
	waitFor(t, "后端恢复健康", func() bool { return rh.proxies[0].up.Load() })
	if rh.proxies[0].failures.Load() != 0 || rh.proxies[0].coolUntil.Load() != 0 {
		t.Fatal("恢复后被动失败计数与冷却应清零")
	}
}

// TestHealthProberHTTP http 探测：5xx 摘除，恢复 200 后重新上线。
func TestHealthProberHTTP(t *testing.T) {
	var code atomic.Int32
	code.Store(http.StatusOK)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(int(code.Load()))
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	h, err := newReverseHandler(config.SubRule{ID: "hh", Name: "hh", Backends: []string{backend.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	rh := h.(*reverseHandler)
	rh.configureHealth(HealthCheckConf{Enabled: true, Type: "http", Path: "/healthz", IntervalSeconds: 1, TimeoutSeconds: 1, Fall: 1, Rise: 2}, config.SubRule{ID: "hh"})
	defer rh.shutdown()

	waitFor(t, "首轮探测成功", func() bool { return rh.proxies[0].lastCheckUnix.Load() > 0 })
	if !rh.proxies[0].up.Load() {
		t.Fatal("200 探测应判定后端健康")
	}
	code.Store(http.StatusInternalServerError)
	waitFor(t, "5xx 后端被摘除", func() bool { return !rh.proxies[0].up.Load() })
	// rise=2：单次成功不足以恢复
	code.Store(http.StatusOK)
	waitFor(t, "连续两次成功后恢复", func() bool { return rh.proxies[0].up.Load() })
}

// TestPassiveFailureMarksDownWithActiveCheck 开启主动检查时，被动连续连接失败立即摘除，
// 且冷却期过后也不会自动回到轮询（恢复必须探测成功）。
func TestPassiveFailureMarksDownWithActiveCheck(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "alive")
	}))
	defer alive.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadURL := "http://" + l.Addr().String()
	_ = l.Close()

	h, err := newReverseHandler(config.SubRule{ID: "pf", Name: "pf", Backends: []string{deadURL, alive.URL}}, newTestRingLog())
	if err != nil {
		t.Fatal(err)
	}
	rh := h.(*reverseHandler)
	rh.activeCheck.Store(true) // 不启动探测协程，单独验证被动路径

	dead := rh.proxies[0]
	dead.noteFailure()
	dead.noteFailure()
	if dead.up.Load() {
		t.Fatal("被动连续失败应立即摘除后端")
	}
	// 冷却期过后仍不参与轮询
	dead.coolUntil.Store(time.Now().Add(-time.Second).UnixNano())
	if got := rh.pick(nil); got != rh.proxies[1] {
		t.Fatal("主动检查下冷却期过后不应自动恢复轮询")
	}
	// 全部摘除时回退轮询全部节点
	rh.proxies[1].up.Store(false)
	if got := rh.pick(nil); got == nil {
		t.Fatal("全部摘除时应回退轮询")
	}
}

// TestHealthAPI 健康检查配置 API：设置/读取/校验/删除，并清理已删规则的配置。
func TestHealthAPI(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: freeRuleAPIAddr(t),
		Rules: []config.SubRule{
			{ID: "r1", Name: "反代", Type: "reverse", Enabled: true, FrontendPath: "/", Backends: []string{backend.URL}},
			{ID: "r2", Name: "跳转", Type: "redirect", Enabled: true, FrontendPath: "/old", RedirectURL: "https://example.com"},
		},
	})
	svc.Start()
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, svc)

	// 非法配置被拒绝
	res := callWebAPI(t, router, http.MethodPut, "/api/sites/s1/rules/r1/health", `{"enabled":true,"intervalSeconds":1}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("非法健康检查配置应为 400, got %d", res.Code)
	}
	// 非反代规则不支持
	res = callWebAPI(t, router, http.MethodPut, "/api/sites/s1/rules/r2/health", `{"enabled":true}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("redirect 规则应为 400, got %d", res.Code)
	}
	// 不存在规则 404
	res = callWebAPI(t, router, http.MethodPut, "/api/sites/s1/rules/nope/health", `{"enabled":true}`)
	if res.Code != http.StatusNotFound {
		t.Fatalf("未知规则应为 404, got %d", res.Code)
	}

	res = callWebAPI(t, router, http.MethodPut, "/api/sites/s1/rules/r1/health", `{"enabled":true,"type":"tcp","intervalSeconds":5,"timeoutSeconds":2}`)
	if res.Code != http.StatusOK {
		t.Fatalf("设置健康检查失败 %d: %s", res.Code, res.Body.String())
	}
	if got := svc.HealthConf("r1"); !got.Enabled || got.Type != "tcp" || got.Fall != 2 || got.Rise != 1 {
		t.Fatalf("配置未生效或默认值错误: %+v", got)
	}
	if got := newHealthStore(cfg.Dir()).confFor("r1"); !got.Enabled {
		t.Fatal("配置应已落盘")
	}

	// 触发一次请求以构建处理器，然后读取健康状态
	code, _, _ := mustGet(t, httpClient(), "http://"+svc.ListenAddr("s1")+"/", nil)
	if code != http.StatusOK {
		t.Fatalf("请求应为 200, got %d", code)
	}
	res = callWebAPI(t, router, http.MethodGet, "/api/sites/s1/rules/r1/health", "")
	if res.Code != http.StatusOK {
		t.Fatalf("读取健康状态失败 %d", res.Code)
	}
	var view struct {
		Data struct {
			Conf     HealthCheckConf `json:"conf"`
			Backends []BackendHealth `json:"backends"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Data.Conf.Enabled || len(view.Data.Backends) != 1 || !view.Data.Backends[0].Up || !view.Data.Backends[0].ActiveCheck {
		t.Fatalf("健康视图错误: %+v", view.Data)
	}

	// 统计接口附带后端健康
	res = callWebAPI(t, router, http.MethodGet, "/api/sites/stats", "")
	var stats struct {
		Data map[string]struct {
			Rules map[string]struct {
				Backends []BackendHealth `json:"backends"`
			} `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.Data["s1"].Rules["r1"].Backends) != 1 {
		t.Fatalf("统计接口应附带后端健康: %s", res.Body.String())
	}

	// 删除后禁用并停止探测
	res = callWebAPI(t, router, http.MethodDelete, "/api/sites/s1/rules/r1/health", "")
	if res.Code != http.StatusOK {
		t.Fatalf("删除健康检查失败 %d", res.Code)
	}
	if svc.HealthConf("r1").Enabled {
		t.Fatal("删除后配置应为关闭")
	}
	svc.mu.Lock()
	ss := svc.sites["s1"]
	svc.mu.Unlock()
	ss.handlerMu.Lock()
	rh := ss.revHandler["r1"].(*reverseHandler)
	ss.handlerMu.Unlock()
	if rh.activeCheck.Load() {
		t.Fatal("删除后运行中的探测应已停止")
	}
}
