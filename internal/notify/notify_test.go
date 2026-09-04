package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func setWebhook(cfg *config.Config, wc config.WebhookConf) {
	cfg.Lock()
	cfg.Settings.Webhook = wc
	cfg.Unlock()
}

// capturedReq 记录 mock 服务器收到的一次请求。
type capturedReq struct {
	Method      string
	Path        string
	ContentType string
	Form        map[string]string
	JSON        map[string]string
}

// waitReq 等待 mock 服务器收到请求，超时则失败。
func waitReq(t *testing.T, ch <-chan capturedReq) capturedReq {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal(" mock 服务器未收到请求")
		return capturedReq{}
	}
}

func captureServer(t *testing.T) (*httptest.Server, <-chan capturedReq) {
	t.Helper()
	ch := make(chan capturedReq, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap := capturedReq{Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type")}
		if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
			r.ParseForm()
			cap.Form = map[string]string{
				"title": r.Form.Get("title"),
				"desp":  r.Form.Get("desp"),
			}
		} else {
			json.NewDecoder(r.Body).Decode(&cap.JSON)
		}
		ch <- cap
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts, ch
}

func TestServerchanFormat(t *testing.T) {
	ts, ch := captureServer(t)
	cfg := newTestConfig(t)
	setWebhook(cfg, config.WebhookConf{Enabled: true, Type: "serverchan", Key: "SCTKEY", Events: []string{"ddns"}})
	n := New(cfg)
	n.serverchanBase = ts.URL

	n.Notify("ddns", "标题A", "内容B")
	got := waitReq(t, ch)
	if got.Method != http.MethodPost {
		t.Errorf("serverchan 应 POST，得到 %s", got.Method)
	}
	if got.Path != "/SCTKEY.send" {
		t.Errorf("serverchan 路径错误: %s", got.Path)
	}
	if got.Form["title"] != "标题A" || got.Form["desp"] != "内容B" {
		t.Errorf("serverchan 表单错误: %+v", got.Form)
	}
}

func TestBarkFormat(t *testing.T) {
	ts, ch := captureServer(t)
	cfg := newTestConfig(t)
	setWebhook(cfg, config.WebhookConf{Enabled: true, Type: "bark", Key: "BARKKEY", Events: []string{"cert"}})
	n := New(cfg)
	n.barkBase = ts.URL

	n.Notify("cert", "标题A", "内容B")
	got := waitReq(t, ch)
	if got.Method != http.MethodPost || got.Path != "/BARKKEY" {
		t.Errorf("bark 请求错误: %s %s", got.Method, got.Path)
	}
	if got.ContentType != "application/json" {
		t.Errorf("bark Content-Type 错误: %s", got.ContentType)
	}
	if got.JSON["title"] != "标题A" || got.JSON["body"] != "内容B" {
		t.Errorf("bark JSON 错误: %+v", got.JSON)
	}
}

func TestTelegramFormat(t *testing.T) {
	ts, ch := captureServer(t)
	cfg := newTestConfig(t)
	setWebhook(cfg, config.WebhookConf{Enabled: true, Type: "telegram", Key: "TOKEN", ChatID: "12345", Events: []string{"ddns"}})
	n := New(cfg)
	n.telegramBase = ts.URL

	n.Notify("ddns", "标题A", "内容B")
	got := waitReq(t, ch)
	if got.Method != http.MethodPost || got.Path != "/botTOKEN/sendMessage" {
		t.Errorf("telegram 请求错误: %s %s", got.Method, got.Path)
	}
	if got.JSON["chat_id"] != "12345" {
		t.Errorf("telegram chat_id 错误: %+v", got.JSON)
	}
	if got.JSON["text"] != "标题A\n内容B" {
		t.Errorf("telegram text 错误: %q", got.JSON["text"])
	}
}

func TestCustomFormat(t *testing.T) {
	ts, ch := captureServer(t)
	cfg := newTestConfig(t)
	setWebhook(cfg, config.WebhookConf{Enabled: true, Type: "custom", URL: ts.URL + "/hook", Events: []string{"ddns", "cert"}})
	n := New(cfg)

	n.Notify("cert", "标题A", "内容B")
	got := waitReq(t, ch)
	if got.Method != http.MethodPost || got.Path != "/hook" {
		t.Errorf("custom 请求错误: %s %s", got.Method, got.Path)
	}
	if got.ContentType != "application/json" {
		t.Errorf("custom Content-Type 错误: %s", got.ContentType)
	}
	if got.JSON["title"] != "标题A" || got.JSON["content"] != "内容B" {
		t.Errorf("custom JSON 错误: %+v", got.JSON)
	}
}

// TestEventFilter 未启用或未订阅事件时不发请求。
func TestEventFilter(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer ts.Close()

	cases := []struct {
		name string
		wc   config.WebhookConf
	}{
		{"未启用", config.WebhookConf{Enabled: false, Type: "custom", URL: ts.URL, Events: []string{"ddns"}}},
		{"未订阅该事件", config.WebhookConf{Enabled: true, Type: "custom", URL: ts.URL, Events: []string{"cert"}}},
		{"事件列表为空", config.WebhookConf{Enabled: true, Type: "custom", URL: ts.URL}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := newTestConfig(t)
			setWebhook(cfg, c.wc)
			n := New(cfg)
			n.Notify("ddns", "t", "c")
			time.Sleep(150 * time.Millisecond)
			if got := atomic.LoadInt32(&hits); got != 0 {
				t.Fatalf("%s：不应发送请求，实际 %d 次", c.name, got)
			}
		})
	}
}

// TestNotifyAsync Notify 应立即返回，不阻塞调用方。
func TestNotifyAsync(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // 阻塞直到测试放行
	}))
	defer ts.Close()
	defer close(release)

	cfg := newTestConfig(t)
	setWebhook(cfg, config.WebhookConf{Enabled: true, Type: "custom", URL: ts.URL, Events: []string{"ddns"}})
	n := New(cfg)

	start := time.Now()
	n.Notify("ddns", "t", "c")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("Notify 阻塞了调用方: %v", elapsed)
	}
}

// TestSendFailure 发送失败返回错误而不 panic。
func TestSendFailure(t *testing.T) {
	cfg := newTestConfig(t)
	n := New(cfg)
	if err := n.send(config.WebhookConf{Type: "custom", URL: "http://127.0.0.1:1/x"}, "t", "c"); err == nil {
		t.Fatal("连接失败应返回错误")
	}
	if err := n.send(config.WebhookConf{Type: "unknown"}, "t", "c"); err == nil {
		t.Fatal("未知类型应返回错误")
	}
	if err := n.send(config.WebhookConf{Type: "serverchan"}, "t", "c"); err == nil {
		t.Fatal("Key 为空应返回错误")
	}
}
