package acme

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"luckyx/internal/api"
	"luckyx/internal/config"
)

// newTestRouter 构造带临时配置目录的测试路由。
func newTestRouter(t *testing.T) (*config.Config, *Manager, chi.Router) {
	t.Helper()
	cfg := newTestConfig(t)
	cfg.Lock()
	cfg.Providers = append(cfg.Providers, config.DNSProviderConf{
		ID: "aliyun-1", Type: "aliyun", Key: "k", Secret: "s",
	})
	cfg.Unlock()
	m := NewManager(cfg)
	r := chi.NewRouter()
	RegisterRoutes(r, cfg, m)
	return cfg, m, r
}

// doReq 发请求并解析统一响应。
func doReq(t *testing.T, r chi.Router, method, path string, body interface{}) (int, api.Response) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var resp api.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

func TestCertCRUD(t *testing.T) {
	cfg, m, r := newTestRouter(t)

	// 新增
	newCert := config.CertConf{
		Name:       "测试证书",
		Enabled:    true,
		Domains:    []string{"*.example.com", "example.com"},
		ProviderID: "aliyun-1",
		Email:      "a@b.com",
	}
	_, resp := doReq(t, r, http.MethodPost, "/api/certs", newCert)
	if resp.Code != 0 {
		t.Fatalf("新增失败: %s", resp.Msg)
	}
	data, _ := json.Marshal(resp.Data)
	var created config.CertConf
	json.Unmarshal(data, &created)
	if created.ID == "" {
		t.Fatal("新增后 ID 为空")
	}

	// 引用不存在的凭据应被拒绝
	_, resp = doReq(t, r, http.MethodPost, "/api/certs", config.CertConf{
		Name: "bad", Domains: []string{"a.com"}, ProviderID: "nope",
	})
	if resp.Code == 0 {
		t.Fatal("引用不存在凭据应失败")
	}

	// 列表
	_, resp = doReq(t, r, http.MethodGet, "/api/certs", nil)
	if resp.Code != 0 {
		t.Fatalf("列表失败: %s", resp.Msg)
	}
	data, _ = json.Marshal(resp.Data)
	var views []certView
	json.Unmarshal(data, &views)
	if len(views) != 1 || views[0].Status != "pending" {
		t.Fatalf("列表异常: %+v", views)
	}

	// 修改
	created.Name = "改名"
	_, resp = doReq(t, r, http.MethodPut, "/api/certs/"+created.ID, created)
	if resp.Code != 0 {
		t.Fatalf("修改失败: %s", resp.Msg)
	}
	// 修改不存在的 ID
	_, resp = doReq(t, r, http.MethodPut, "/api/certs/nonexistent", created)
	if resp.Code != 404 {
		t.Fatalf("修改不存在证书应 404，得到 %d", resp.Code)
	}

	// 开关
	_, resp = doReq(t, r, http.MethodPost, "/api/certs/"+created.ID+"/toggle", nil)
	if resp.Code != 0 {
		t.Fatalf("开关失败: %s", resp.Msg)
	}
	cfg.RLock()
	enabled := cfg.Certs[0].Enabled
	cfg.RUnlock()
	if enabled {
		t.Fatal("toggle 后应为禁用")
	}

	// 下载：未申请时应 404
	_, resp = doReq(t, r, http.MethodGet, "/api/certs/"+created.ID+"/download?part=cert", nil)
	if resp.Code != 404 {
		t.Fatalf("未申请时下载应 404，得到 %d", resp.Code)
	}
	// part 参数校验
	_, resp = doReq(t, r, http.MethodGet, "/api/certs/"+created.ID+"/download?part=bad", nil)
	if resp.Code != 400 {
		t.Fatalf("非法 part 应 400，得到 %d", resp.Code)
	}

	// 落盘一份证书后测试下载与删除清理文件
	c := writeCertFiles(t, cfg, created.ID, []string{"*.example.com"}, time.Now().Add(90*24*time.Hour))
	cfg.Lock()
	cfg.Certs[0].CertFile = c.CertFile
	cfg.Certs[0].KeyFile = c.KeyFile
	cfg.Certs[0].NotAfter = c.NotAfter
	cfg.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/certs/"+created.ID+"/download?part=key", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("下载私钥失败: %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), ".key") {
		t.Fatalf("下载文件名异常: %s", rec.Header().Get("Content-Disposition"))
	}
	if !strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatal("下载内容不是 PEM 私钥")
	}

	// 删除应同时删除磁盘文件
	certFile, keyFile := m.certPath(&c)
	_, resp = doReq(t, r, http.MethodDelete, "/api/certs/"+created.ID, nil)
	if resp.Code != 0 {
		t.Fatalf("删除失败: %s", resp.Msg)
	}
	if _, err := os.Stat(certFile); !os.IsNotExist(err) {
		t.Fatal("删除后证书文件仍存在")
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatal("删除后私钥文件仍存在")
	}
	// 配置已持久化
	reloaded, err := config.Load(cfg.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Certs) != 0 {
		t.Fatal("删除未持久化到配置文件")
	}
}

func TestObtainAsync(t *testing.T) {
	_, _, r := newTestRouter(t)
	_, resp := doReq(t, r, http.MethodPost, "/api/certs/nonexistent/obtain", nil)
	if resp.Code != 404 {
		t.Fatalf("不存在证书 obtain 应 404，得到 %d", resp.Code)
	}
	// 存在的证书应立即返回 obtaining=true（申请在后台进行，预期失败但不阻塞）
	_, resp = doReq(t, r, http.MethodPost, "/api/certs", config.CertConf{
		Name: "t", Enabled: true, Domains: []string{"a.example.com"}, ProviderID: "aliyun-1",
	})
	data, _ := json.Marshal(resp.Data)
	var created config.CertConf
	json.Unmarshal(data, &created)

	req := httptest.NewRequest(http.MethodPost, "/api/certs/"+created.ID+"/obtain", nil)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		r.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("obtain 接口未异步返回")
	}
}

func TestStatusOf(t *testing.T) {
	now := time.Now()
	cases := []struct {
		cert config.CertConf
		want string
	}{
		{config.CertConf{}, "pending"},
		{config.CertConf{LastError: "x"}, "error"},
		{config.CertConf{NotAfter: "bad"}, "error"},
		{config.CertConf{NotAfter: now.Add(-time.Hour).Format(time.RFC3339)}, "expired"},
		{config.CertConf{NotAfter: now.Add(10 * 24 * time.Hour).Format(time.RFC3339)}, "expiring"},
		{config.CertConf{NotAfter: now.Add(60 * 24 * time.Hour).Format(time.RFC3339)}, "ok"},
	}
	for _, c := range cases {
		if got := statusOf(&c.cert, now); got != c.want {
			t.Errorf("statusOf(%+v) = %s, want %s", c.cert, got, c.want)
		}
	}
}
