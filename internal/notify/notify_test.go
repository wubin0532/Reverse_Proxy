package notify

import (
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

func newTestConfig(types []string) *config.Config {
	cfg := &config.Config{}
	cfg.Settings.Notifications.Types = types
	cfg.Settings.Notifications.Telegram = config.TelegramNotification{
		Enabled:  true,
		BotToken: "12345:abcdefghijklmnopqrstuvwxyz_ABCD",
		ChatID:   "123456",
	}
	return cfg
}

// TestManagerMatch 验证过滤规则：空类型列表只推 warn/error，非空按前缀匹配。
func TestManagerMatch(t *testing.T) {
	m := NewManager(newTestConfig(nil))
	cases := []struct {
		ev   Event
		want bool
	}{
		{Event{Type: TypeCertObtainSuccess, Level: LevelInfo}, false},
		{Event{Type: TypeDDNSUpdateFailed, Level: LevelError}, true},
		{Event{Type: TypeSiteListenError, Level: LevelWarn}, true},
	}
	for _, c := range cases {
		if got := m.match(c.ev); got != c.want {
			t.Errorf("空类型列表 match(%+v) = %v，期望 %v", c.ev, got, c.want)
		}
	}

	m2 := NewManager(newTestConfig([]string{"cert"}))
	if !m2.match(Event{Type: TypeCertObtainSuccess, Level: LevelInfo}) {
		t.Errorf("前缀 cert 应匹配 cert.obtain_success（含 info 级）")
	}
	if m2.match(Event{Type: TypeDDNSUpdateFailed, Level: LevelError}) {
		t.Errorf("前缀 cert 不应匹配 ddns 事件")
	}
}

type retrySender struct{ calls atomic.Int32 }

func (s *retrySender) Name() string     { return "test" }
func (s *retrySender) Configured() bool { return true }
func (s *retrySender) Send(Event) error {
	if s.calls.Add(1) < 3 {
		return errTemporary
	}
	return nil
}

var errTemporary = &temporaryError{}

type temporaryError struct{}

func (*temporaryError) Error() string { return "temporary" }

func TestChannelSendRetry(t *testing.T) {
	defer func(orig []time.Duration) { notificationRetryBackoff = orig }(notificationRetryBackoff)
	notificationRetryBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	sender := &retrySender{}
	if err := sendWithRetry(sender, Event{Type: TypeTest}); err != nil {
		t.Fatalf("重试后应发送成功: %v", err)
	}
	if got := sender.calls.Load(); got != 3 {
		t.Fatalf("应请求 3 次，实际 %d", got)
	}
}

func TestDisabledChannelDoesNotQueue(t *testing.T) {
	cfg := newTestConfig(nil)
	cfg.Settings.Notifications.Telegram.Enabled = false
	m := NewManager(cfg)
	m.Handle(Event{Type: TypeDDNSUpdateFailed, Level: LevelError})
	if got := len(m.queue); got != 0 {
		t.Fatalf("禁用全部渠道时不应入队，实际 %d", got)
	}
}
