package notify

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

// doReq 发请求并解析统一响应。
func doReq(t *testing.T, r chi.Router, method, path string, body interface{}) api.Response {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var resp api.Response
	json.NewDecoder(rec.Body).Decode(&resp)
	return resp
}

func newTestRouter(t *testing.T) (*config.Config, *Notifier, chi.Router) {
	t.Helper()
	cfg := newTestConfig(t)
	n := New(cfg)
	r := chi.NewRouter()
	RegisterRoutes(r, cfg, n)
	return cfg, n, r
}

func TestWebhookAPI(t *testing.T) {
	cfg, _, r := newTestRouter(t)

	// 初始为空配置
	resp := doReq(t, r, http.MethodGet, "/api/settings/webhook", nil)
	if resp.Code != 0 {
		t.Fatalf("GET 失败: %s", resp.Msg)
	}

	// 非法类型被拒绝
	resp = doReq(t, r, http.MethodPut, "/api/settings/webhook", config.WebhookConf{
		Enabled: true, Type: "wechat", Events: []string{"ddns"},
	})
	if resp.Code == 0 {
		t.Fatal("非法类型应被拒绝")
	}

	// custom 类型缺 URL 被拒绝
	resp = doReq(t, r, http.MethodPut, "/api/settings/webhook", config.WebhookConf{
		Enabled: true, Type: "custom", Events: []string{"ddns"},
	})
	if resp.Code == 0 {
		t.Fatal("custom 缺 URL 应被拒绝")
	}

	// 正常保存
	want := config.WebhookConf{Enabled: true, Type: "telegram", Key: "TOKEN", ChatID: "1", Events: []string{"ddns", "cert"}}
	resp = doReq(t, r, http.MethodPut, "/api/settings/webhook", want)
	if resp.Code != 0 {
		t.Fatalf("PUT 失败: %s", resp.Msg)
	}
	cfg.RLock()
	got := cfg.Settings.Webhook
	cfg.RUnlock()
	if got.Type != want.Type || got.Key != want.Key || len(got.Events) != 2 {
		t.Fatalf("保存内容不符: %+v", got)
	}
	// 已持久化
	reloaded, err := config.Load(cfg.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Settings.Webhook.Key != "TOKEN" {
		t.Fatal("配置未持久化")
	}
}

// TestWebhookTestAPI 测试接口同步发送并返回结果。
func TestWebhookTestAPI(t *testing.T) {
	ts, ch := captureServer(t)
	cfg, n, r := newTestRouter(t)
	setWebhook(cfg, config.WebhookConf{Enabled: true, Type: "bark", Key: "K", Events: []string{"ddns"}})
	n.barkBase = ts.URL

	resp := doReq(t, r, http.MethodPost, "/api/settings/webhook/test", nil)
	if resp.Code != 0 {
		t.Fatalf("测试发送应成功: %s", resp.Msg)
	}
	got := waitReq(t, ch)
	if !strings.Contains(got.JSON["title"], "测试") {
		t.Fatalf("测试消息标题异常: %+v", got.JSON)
	}

	// 对端返回非 2xx 时应返回失败原因
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusBadRequest)
	}))
	defer bad.Close()
	n.barkBase = bad.URL
	resp = doReq(t, r, http.MethodPost, "/api/settings/webhook/test", nil)
	if resp.Code == 0 || !strings.Contains(resp.Msg, "400") {
		t.Fatalf("应返回失败原因，得到 code=%d msg=%s", resp.Code, resp.Msg)
	}
}
