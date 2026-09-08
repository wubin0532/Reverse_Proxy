package notify

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

const testTelegramToken = "12345:abcdefghijklmnopqrstuvwxyz_ABCD"

func TestTelegramSendMessage(t *testing.T) {
	var got struct {
		ChatID          string `json:"chat_id"`
		Text            string `json:"text"`
		MessageThreadID int64  `json:"message_thread_id"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+testTelegramToken+"/sendMessage" {
			t.Errorf("请求路径错误: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("请求方法或内容类型错误")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("Telegram 请求体解析失败: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Settings.Notifications.Telegram = config.TelegramNotification{
		Enabled: true, BotToken: testTelegramToken, ChatID: "-1001234567890", MessageThreadID: 42,
	}
	sender := NewTelegramSender(cfg)
	sender.baseURL = srv.URL
	event := Event{Type: TypeDDNSUpdateFailed, Level: LevelError, Entity: "home", Message: "公网地址更新失败", Time: time.Date(2026, 9, 8, 9, 30, 0, 0, time.Local)}
	if err := sender.Send(event); err != nil {
		t.Fatalf("发送 Telegram 消息失败: %v", err)
	}
	if got.ChatID != "-1001234567890" || got.MessageThreadID != 42 {
		t.Fatalf("目标参数不正确: %+v", got)
	}
	for _, want := range []string{"andey-proxy 通知", "级别：错误", "类型：ddns.update_failed", "对象：home", "内容：公网地址更新失败"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("消息缺少 %q: %s", want, got.Text)
		}
	}
}

func TestTelegramErrorDoesNotLeakToken(t *testing.T) {
	cfg := &config.Config{}
	cfg.Settings.Notifications.Telegram = config.TelegramNotification{Enabled: true, BotToken: testTelegramToken, ChatID: "1"}
	sender := NewTelegramSender(cfg)
	sender.baseURL = "http://127.0.0.1:1"
	err := sender.Send(Event{Type: TypeTest, Level: LevelInfo, Message: "test"})
	if err == nil {
		t.Fatal("不可达地址应返回错误")
	}
	if strings.Contains(err.Error(), testTelegramToken) {
		t.Fatalf("错误信息泄露 Bot Token: %v", err)
	}
}

func TestValidateTelegramConfig(t *testing.T) {
	valid := config.TelegramNotification{Enabled: true, BotToken: testTelegramToken, ChatID: "@channel_name"}
	if err := ValidateTelegramConfig(valid, true); err != nil {
		t.Fatalf("有效配置被拒绝: %v", err)
	}
	invalid := []config.TelegramNotification{
		{Enabled: true, ChatID: "123"},
		{Enabled: true, BotToken: "bad", ChatID: "123"},
		{Enabled: true, BotToken: testTelegramToken, ChatID: "bad id"},
		{BotToken: testTelegramToken, ChatID: "123", MessageThreadID: -1},
	}
	for _, item := range invalid {
		if err := ValidateTelegramConfig(item, true); err == nil {
			t.Errorf("无效配置未被拒绝: %+v", item)
		}
	}
}

func TestSettingsResponseHidesTelegramToken(t *testing.T) {
	settings := config.NotificationSettings{Telegram: config.TelegramNotification{Enabled: true, BotToken: testTelegramToken, ChatID: "123"}}
	raw, err := json.Marshal(settingsResponse(settings))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testTelegramToken) || strings.Contains(string(raw), "botToken\"") {
		t.Fatalf("读取响应泄露 Bot Token: %s", raw)
	}
	if !strings.Contains(string(raw), `"botTokenConfigured":true`) {
		t.Fatalf("读取响应缺少凭据状态: %s", raw)
	}
}

func TestSettingsResponseUsesEmptyArrayForTypes(t *testing.T) {
	raw, err := json.Marshal(settingsResponse(config.NotificationSettings{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"types":[]`) {
		t.Fatalf("空订阅类型必须返回数组: %s", raw)
	}
}

func TestNotificationSettingsAPIPreservesAndHidesToken(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Settings.Notifications.Telegram = config.TelegramNotification{Enabled: true, BotToken: testTelegramToken, ChatID: "123"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := &handler{cfg: cfg}
	body := []byte(`{"types":["ddns"],"telegram":{"enabled":true,"botToken":"","clearBotToken":false,"chatId":"456","messageThreadId":0}}`)
	recorder := httptest.NewRecorder()
	h.putSettings(recorder, httptest.NewRequest(http.MethodPut, "/api/notifications/settings", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("保存设置返回 %d: %s", recorder.Code, recorder.Body.String())
	}
	cfg.RLock()
	got := cfg.Settings.Notifications.Telegram
	cfg.RUnlock()
	if got.BotToken != testTelegramToken || got.ChatID != "456" {
		t.Fatalf("留空时未保留 Token 或 Chat ID 未更新: %+v", got)
	}
	if strings.Contains(recorder.Body.String(), testTelegramToken) {
		t.Fatalf("保存响应泄露 Bot Token: %s", recorder.Body.String())
	}
}

func TestLegacyNotificationRoutesRemoved(t *testing.T) {
	cfg := &config.Config{}
	bus := NewBus()
	defer bus.Close()
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, bus, NewManager(cfg))
	legacy := httptest.NewRecorder()
	router.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/api/notify/settings", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("旧通知接口应已移除，实际状态码 %d", legacy.Code)
	}
	current := httptest.NewRecorder()
	router.ServeHTTP(current, httptest.NewRequest(http.MethodGet, "/api/notifications/settings", nil))
	if current.Code != http.StatusOK {
		t.Fatalf("新通知接口不可用，实际状态码 %d", current.Code)
	}
}

func TestDeleteTelegramChannelClearsCredentialsAndKeepsTypes(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Settings.Notifications = config.NotificationSettings{
			Types: []string{"ddns", "site"},
			Telegram: config.TelegramNotification{
				Enabled: true, BotToken: testTelegramToken, ChatID: "123", MessageThreadID: 42,
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	defer bus.Close()
	router := chi.NewRouter()
	RegisterRoutes(router, cfg, bus, NewManager(cfg))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/notifications/channels/telegram", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("删除渠道返回 %d: %s", recorder.Code, recorder.Body.String())
	}
	cfg.RLock()
	settings := cfg.Settings.Notifications
	cfg.RUnlock()
	if settings.Telegram != (config.TelegramNotification{}) {
		t.Fatalf("Telegram 配置未清空: %+v", settings.Telegram)
	}
	if len(settings.Types) != 2 || settings.Types[0] != "ddns" || settings.Types[1] != "site" {
		t.Fatalf("订阅类型不应被删除: %+v", settings.Types)
	}
	if strings.Contains(recorder.Body.String(), testTelegramToken) || !strings.Contains(recorder.Body.String(), `"configured":false`) {
		t.Fatalf("删除响应异常: %s", recorder.Body.String())
	}
}
