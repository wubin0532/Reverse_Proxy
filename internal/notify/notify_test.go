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

// TestRecentRingBuffer 验证环形缓冲容量与新的在前的返回顺序。
func TestRecentRingBuffer(t *testing.T) {
	b := NewBus()
	defer b.Close()
	for i := 0; i < recentCapacity+50; i++ {
		b.Publish(Event{Type: TypeDDNSUpdateFailed, Level: LevelError, Message: string(rune('a' + i%26))})
	}
	got := b.Recent(0)
	if len(got) != 20 { // 默认 20 条
		t.Fatalf("默认应返回 20 条，实际 %d", len(got))
	}
	all := b.Recent(recentCapacity * 2) // 超过容量应按容量截断
	if len(all) != recentCapacity {
		t.Fatalf("最多返回 %d 条，实际 %d", recentCapacity, len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Time.Before(all[i].Time) {
			t.Fatalf("事件应按时间倒序返回")
		}
	}
}

// TestPublishDropWhenQueueFull blocks a subscriber before filling the queue,
// so dispatch scheduling cannot change the expected drop count.
func TestPublishDropWhenQueueFull(t *testing.T) {
	b := NewBus()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer func() { b.Close(); close(release) }()
	b.Subscribe(func(Event) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	b.Publish(Event{Type: TypeTest})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not start")
	}
	for i := 0; i < queueCapacity+10; i++ {
		b.Publish(Event{Type: TypeSiteListenError, Level: LevelError, Message: "x"})
	}
	if d := b.Dropped(); d != 10 {
		t.Fatalf("应丢弃 10 条，实际 %d", d)
	}
	// 环形缓冲不受分发队列影响，仍保留最近事件
	if got := b.Recent(recentCapacity); len(got) != recentCapacity {
		t.Fatalf("环形缓冲应保留 %d 条，实际 %d", recentCapacity, len(got))
	}
}

// TestPublishNilDefault 未设置默认总线时包级 Publish 不应 panic。
func TestPublishNilDefault(t *testing.T) {
	SetDefault(nil)
	Publish(Event{Type: TypeTest})
	if got := Recent(10); len(got) != 0 {
		t.Fatalf("默认总线为空时 Recent 应返回空")
	}
}

func newTestConfig(hookURL string, types []string) *config.Config {
	cfg := &config.Config{}
	cfg.Settings.NotifyWebhookURL = hookURL
	cfg.Settings.NotifyTypes = types
	return cfg
}

// TestWebhookMatch 验证过滤规则：空类型列表只推 warn/error，非空按前缀匹配。
func TestWebhookMatch(t *testing.T) {
	w := NewWebhook(newTestConfig("https://example.com/hook", nil))
	cases := []struct {
		ev   Event
		want bool
	}{
		{Event{Type: TypeCertObtainSuccess, Level: LevelInfo}, false},
		{Event{Type: TypeDDNSUpdateFailed, Level: LevelError}, true},
		{Event{Type: TypeSiteListenError, Level: LevelWarn}, true},
	}
	for _, c := range cases {
		if got := w.match(c.ev); got != c.want {
			t.Errorf("空类型列表 match(%+v) = %v，期望 %v", c.ev, got, c.want)
		}
	}

	w2 := NewWebhook(newTestConfig("https://example.com/hook", []string{"cert"}))
	if !w2.match(Event{Type: TypeCertObtainSuccess, Level: LevelInfo}) {
		t.Errorf("前缀 cert 应匹配 cert.obtain_success（含 info 级）")
	}
	if w2.match(Event{Type: TypeDDNSUpdateFailed, Level: LevelError}) {
		t.Errorf("前缀 cert 不应匹配 ddns 事件")
	}

	// 未配置 URL 时一律不匹配（禁用）
	w3 := NewWebhook(newTestConfig("", nil))
	if w3.match(Event{Type: TypeDDNSUpdateFailed, Level: LevelError}) {
		t.Errorf("空 Webhook URL 应禁用推送")
	}
}

// TestWebhookSendRetry 首次失败按退避重试，第三次成功。
func TestWebhookSendRetry(t *testing.T) {
	defer func(orig []time.Duration) { retryBackoff = orig }(retryBackoff)
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var ev Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			t.Errorf("请求体应为事件 JSON: %v", err)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type 应为 application/json，实际 %s", ct)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := NewWebhook(newTestConfig(srv.URL, nil))
	if err := w.send(srv.URL, Event{Type: TypeTest, Level: LevelInfo, Message: "测试"}); err != nil {
		t.Fatalf("重试后应发送成功: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("应请求 3 次，实际 %d", got)
	}
}

// TestWebhookSendAllFail 全部尝试失败后返回错误。
func TestWebhookSendAllFail(t *testing.T) {
	defer func(orig []time.Duration) { retryBackoff = orig }(retryBackoff)
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	w := NewWebhook(newTestConfig(srv.URL, nil))
	if err := w.send(srv.URL, Event{Type: TypeTest}); err == nil {
		t.Fatalf("持续失败应返回错误")
	}
	if got := calls.Load(); got != webhookMaxAttempts {
		t.Fatalf("应尝试 %d 次，实际 %d", webhookMaxAttempts, got)
	}
}

// TestWebhookEndToEnd 经总线订阅到 HTTP 送达的完整链路。
func TestWebhookEndToEnd(t *testing.T) {
	received := make(chan Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev Event
		_ = json.NewDecoder(r.Body).Decode(&ev)
		received <- ev
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := newTestConfig(srv.URL, nil)
	bus := NewBus()
	defer bus.Close()
	hook := NewWebhook(cfg)
	bus.Subscribe(hook.Handle)

	// info 级默认不推送
	bus.Publish(Event{Type: TypeCertObtainSuccess, Level: LevelInfo, Message: "不应推送"})
	bus.Publish(Event{Type: TypeDDNSUpdateFailed, Level: LevelError, Entity: "home", Message: "更新失败"})

	select {
	case ev := <-received:
		if ev.Type != TypeDDNSUpdateFailed || ev.Entity != "home" {
			t.Fatalf("推送的事件内容不正确: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("5 秒内未收到 Webhook 推送")
	}
}

// TestValidateWebhookURL 校验 URL 规则：拒绝 userinfo、非 http/https 协议与片段。
func TestValidateWebhookURL(t *testing.T) {
	valid := []string{"", "https://example.com/hook?token=abc", "http://192.168.1.10:8080/notify"}
	for _, u := range valid {
		if err := ValidateWebhookURL(u); err != nil {
			t.Errorf("%q 应合法: %v", u, err)
		}
	}
	invalid := []string{
		"https://user:pass@example.com/hook", // userinfo
		"file:///etc/passwd",                 // 非 http/https
		"ftp://example.com/x",
		"https:///no-host",
		"https://example.com/hook#frag",
		"not-a-url",
	}
	for _, u := range invalid {
		if err := ValidateWebhookURL(u); err == nil {
			t.Errorf("%q 应被拒绝", u)
		}
	}
}
