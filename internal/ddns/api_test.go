package ddns

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/config"
)

func TestProviderTestRejectsMissingSavedID(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: cfg}
	req := httptest.NewRequest("POST", "/api/providers/test", bytes.NewBufferString(`{"id":"missing","domain":"www.example.com"}`))
	rec := httptest.NewRecorder()
	h.testProvider(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProviderTestRejectsTypeConfusion(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = []config.DNSProviderConf{{ID: "saved", Type: "aliyun", Key: "key", Secret: "secret"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: cfg}
	req := httptest.NewRequest("POST", "/api/providers/test", bytes.NewBufferString(`{"id":"saved","type":"cloudflare","domain":"www.example.com"}`))
	rec := httptest.NewRecorder()
	h.testProvider(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestValidateTaskWildcardDomain(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = []config.DNSProviderConf{{ID: "p1", Type: "cloudflare", Key: "k"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: cfg}

	base := func(domains ...string) *config.DDNSTask {
		return &config.DDNSTask{
			Name:       "t",
			Domains:    domains,
			ProviderID: "p1",
			IPType:     "ipv4",
			IPSource:   "api",
			APIURL:     "https://example.com/ip",
		}
	}

	for _, d := range []string{"*.example.com", "*.WUBIN.XYZ.", "www.example.com"} {
		if code, msg := h.validateTask(base(d)); code != 0 {
			t.Errorf("domain %q should pass, got code=%d msg=%s", d, code, msg)
		}
	}
	for _, d := range []string{"foo*bar.example.com", "*", "a.*.example.com", "*.*.example.com"} {
		if code, _ := h.validateTask(base(d)); code != 400 {
			t.Errorf("domain %q should be rejected with 400, got code=%d", d, code)
		}
	}
}

// fakeCloudflareAPI 伪造 Cloudflare API：/zones 返回一个 zone，记录查询返回空列表。
// 记录收到的请求数与最后一次 Authorization 头。
func fakeCloudflareAPI(hits *atomic.Int32, lastAuth *atomic.Value) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		lastAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/zones") {
			fmt.Fprint(w, `{"success":true,"result":[{"id":"z1"}]}`)
			return
		}
		fmt.Fprint(w, `{"success":true,"result":[]}`)
	}))
}

func ddnsTestHandler(t *testing.T, providers ...config.DNSProviderConf) *handler {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Providers = providers
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return &handler{cfg: cfg, w: NewWorker(cfg)}
}

func TestProviderTestRejectsEndpointChangeWithoutCredentials(t *testing.T) {
	var hits atomic.Int32
	var lastAuth atomic.Value
	server := fakeCloudflareAPI(&hits, &lastAuth)
	defer server.Close()
	h := ddnsTestHandler(t, config.DNSProviderConf{ID: "cf1", Type: "cloudflare", Key: "saved-token"})
	body := fmt.Sprintf(`{"id":"cf1","endpoint":%q,"domain":"www.example.com"}`, server.URL)
	req := httptest.NewRequest("POST", "/api/providers/test", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.testProvider(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "更换端点后需重新输入凭据") {
		t.Fatalf("unexpected message: %s", rec.Body.String())
	}
	if hits.Load() != 0 {
		t.Fatal("已保存凭据被发往变更后的端点")
	}
}

func TestProviderTestEndpointChangeWithNewCredentials(t *testing.T) {
	var hits atomic.Int32
	var lastAuth atomic.Value
	server := fakeCloudflareAPI(&hits, &lastAuth)
	defer server.Close()
	h := ddnsTestHandler(t, config.DNSProviderConf{ID: "cf1", Type: "cloudflare", Key: "saved-token"})
	body := fmt.Sprintf(`{"id":"cf1","endpoint":%q,"key":"new-token","domain":"www.example.com"}`, server.URL)
	req := httptest.NewRequest("POST", "/api/providers/test", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.testProvider(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if hits.Load() == 0 {
		t.Fatal("未向新端点发出测试请求")
	}
	if got, _ := lastAuth.Load().(string); got != "Bearer new-token" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer new-token")
	}
}

func TestProviderTestEndpointUnchangedBackfillsCredentials(t *testing.T) {
	var hits atomic.Int32
	var lastAuth atomic.Value
	server := fakeCloudflareAPI(&hits, &lastAuth)
	defer server.Close()
	h := ddnsTestHandler(t, config.DNSProviderConf{ID: "cf1", Type: "cloudflare", Key: "saved-token", Endpoint: server.URL})
	for _, tc := range []struct{ name, body string }{
		{"端点未指定", `{"id":"cf1","domain":"www.example.com"}`},
		{"端点与已存一致", fmt.Sprintf(`{"id":"cf1","endpoint":%q,"domain":"www.example.com"}`, server.URL)},
	} {
		req := httptest.NewRequest("POST", "/api/providers/test", bytes.NewBufferString(tc.body))
		rec := httptest.NewRecorder()
		h.testProvider(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: status = %d, body = %s", tc.name, rec.Code, rec.Body.String())
		}
		if got, _ := lastAuth.Load().(string); got != "Bearer saved-token" {
			t.Fatalf("%s: Authorization = %q, want %q", tc.name, got, "Bearer saved-token")
		}
	}
}

func TestUpdateProviderRejectsEndpointChangeWithoutCredentials(t *testing.T) {
	h := ddnsTestHandler(t, config.DNSProviderConf{ID: "cf1", Type: "cloudflare", Key: "saved-token"})
	put := func(body string) *httptest.ResponseRecorder {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "cf1")
		req := httptest.NewRequest("PUT", "/api/providers/cf1", bytes.NewBufferString(body))
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.updateProvider(rec, req)
		return rec
	}
	rec := put(`{"type":"cloudflare","endpoint":"https://evil.example.com/api"}`)
	if rec.Code != 400 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "更换端点后需重新输入凭据") {
		t.Fatalf("unexpected message: %s", rec.Body.String())
	}
	// 配置未被篡改
	h.cfg.RLock()
	saved := h.cfg.Providers[0]
	h.cfg.RUnlock()
	if saved.Key != "saved-token" || saved.Endpoint != "" {
		t.Fatalf("saved provider mutated: %+v", saved)
	}
	// 显式提供新凭据则允许更换端点
	rec = put(`{"type":"cloudflare","endpoint":"https://evil.example.com/api","key":"new-token"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	h.cfg.RLock()
	saved = h.cfg.Providers[0]
	h.cfg.RUnlock()
	if saved.Key != "new-token" || saved.Endpoint != "https://evil.example.com/api" {
		t.Fatalf("provider not updated: %+v", saved)
	}
	// 端点未指定时回填照旧工作
	rec = put(`{"type":"cloudflare","remark":"ok"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	h.cfg.RLock()
	saved = h.cfg.Providers[0]
	h.cfg.RUnlock()
	if saved.Key != "new-token" || saved.Endpoint != "https://evil.example.com/api" || saved.Remark != "ok" {
		t.Fatalf("backfill broken: %+v", saved)
	}
}
