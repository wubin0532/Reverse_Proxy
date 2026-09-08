package webproxy

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func freeRuleAPIAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func callWebAPI(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var payload *bytes.Reader
	if body == "" {
		payload = bytes.NewReader(nil)
	} else {
		payload = bytes.NewReader([]byte(body))
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, payload)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestRuleAPICRUDToggleOrderAndSecretPreservation(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: freeRuleAPIAddr(t),
		Rules: []config.SubRule{
			{ID: "r1", Name: "第一条", Type: "reverse", Enabled: true, FrontendPath: "/", Backends: []string{"http://127.0.0.1:18080"}, BasicAuth: true, AuthUser: "admin", AuthPass: "secret", Headers: map[string]string{"X-Secret": "hidden"}},
			{ID: "r2", Name: "第二条", Type: "redirect", Enabled: true, FrontendPath: "/old", RedirectURL: "https://example.com", RedirectCode: 302},
		},
	})
	svc.Start()
	t.Cleanup(svc.Stop)
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, svc)

	update := `{"name":"第一条修改","type":"reverse","enabled":true,"frontendPath":"/","backends":["http://127.0.0.1:18080"],"basicAuth":true,"authUser":"admin","authPass":"","headers":{"X-Secret":""}}`
	res := callWebAPI(t, router, http.MethodPut, "/api/sites/s1/rules/r1", update)
	if res.Code != http.StatusOK {
		t.Fatalf("更新规则返回 %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret") || strings.Contains(res.Body.String(), "hidden") {
		t.Fatalf("响应泄露了规则密钥: %s", res.Body.String())
	}
	cfg.RLock()
	updated := cfg.Sites[0].Rules[0]
	cfg.RUnlock()
	if updated.Name != "第一条修改" || updated.AuthPass != "secret" || updated.Headers["X-Secret"] != "hidden" {
		t.Fatalf("更新时未保留密钥: %+v", updated)
	}

	res = callWebAPI(t, router, http.MethodPost, "/api/sites/s1/rules/r1/toggle", "")
	if res.Code != http.StatusOK {
		t.Fatalf("切换规则返回 %d: %s", res.Code, res.Body.String())
	}
	cfg.RLock()
	enabled := cfg.Sites[0].Rules[0].Enabled
	cfg.RUnlock()
	if enabled {
		t.Fatal("规则应已停用")
	}

	res = callWebAPI(t, router, http.MethodPut, "/api/sites/s1/rules/order", `{"ruleIds":["r2","r1"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("规则排序返回 %d: %s", res.Code, res.Body.String())
	}
	cfg.RLock()
	firstID := cfg.Sites[0].Rules[0].ID
	cfg.RUnlock()
	if firstID != "r2" {
		t.Fatalf("规则顺序未保存，第一条为 %s", firstID)
	}

	create := `{"name":"文件","type":"fileserver","enabled":true,"frontendPath":"/files","rootDir":"/tmp"}`
	res = callWebAPI(t, router, http.MethodPost, "/api/sites/s1/rules", create)
	if res.Code != http.StatusOK {
		t.Fatalf("新增规则返回 %d: %s", res.Code, res.Body.String())
	}
	cfg.RLock()
	createdID := cfg.Sites[0].Rules[2].ID
	cfg.RUnlock()
	if createdID == "" {
		t.Fatal("新增规则未生成 ID")
	}
	createdStats := svc.statsFor("s1").ruleFor(createdID)
	createdStats.begin()
	createdStats.finish(http.StatusOK, 1, 2)

	res = callWebAPI(t, router, http.MethodDelete, "/api/sites/s1/rules/"+createdID, "")
	if res.Code != http.StatusOK {
		t.Fatalf("删除规则返回 %d: %s", res.Code, res.Body.String())
	}
	cfg.RLock()
	count := len(cfg.Sites[0].Rules)
	cfg.RUnlock()
	if count != 2 {
		t.Fatalf("删除后规则数应为 2，实际 %d", count)
	}
	if _, exists := svc.AllSiteStats()["s1"].Rules[createdID]; exists {
		t.Fatal("删除子规则后仍保留对应统计")
	}
}

func TestRuleAPIRejectsIncompleteOrderWithoutChangingConfig(t *testing.T) {
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{
		ID: "s1", Name: "站点", Enabled: true, Listen: freeRuleAPIAddr(t),
		Rules: []config.SubRule{
			{ID: "r1", Name: "一", Type: "fileserver", Enabled: true, FrontendPath: "/a", RootDir: "/tmp"},
			{ID: "r2", Name: "二", Type: "fileserver", Enabled: true, FrontendPath: "/b", RootDir: "/tmp"},
		},
	})
	svc.Start()
	t.Cleanup(svc.Stop)
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, svc)

	res := callWebAPI(t, router, http.MethodPut, "/api/sites/s1/rules/order", `{"ruleIds":["r1"]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("不完整排序应返回 400，实际 %d: %s", res.Code, res.Body.String())
	}
	cfg.RLock()
	firstID, secondID := cfg.Sites[0].Rules[0].ID, cfg.Sites[0].Rules[1].ID
	cfg.RUnlock()
	if firstID != "r1" || secondID != "r2" {
		t.Fatalf("失败请求改变了配置: %s, %s", firstID, secondID)
	}
}
