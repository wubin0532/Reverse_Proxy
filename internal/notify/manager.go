package notify

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

const notificationQueueCapacity = 64
const notificationMaxAttempts = 3

var notificationRetryBackoff = []time.Duration{time.Second, 2 * time.Second}

// Sender 是单个通知渠道的发送接口，新增渠道无需改动事件总线。
type Sender interface {
	Name() string
	Configured() bool
	Send(Event) error
}

// Manager 根据订阅设置筛选事件，并交给已启用的通知渠道。
type Manager struct {
	cfg       *config.Config
	telegram  *TelegramSender
	queue     chan Event
	droppedMu sync.Mutex
	dropped   int64
}

func NewManager(cfg *config.Config) *Manager {
	m := &Manager{
		cfg:      cfg,
		telegram: NewTelegramSender(cfg),
		queue:    make(chan Event, notificationQueueCapacity),
	}
	go m.worker()
	return m
}

func (m *Manager) senders() []Sender { return []Sender{m.telegram} }

func (m *Manager) Handle(ev Event) {
	if !m.match(ev) || !m.anyConfigured() {
		return
	}
	select {
	case m.queue <- ev:
	default:
		m.droppedMu.Lock()
		m.dropped++
		m.droppedMu.Unlock()
	}
}

func (m *Manager) match(ev Event) bool {
	m.cfg.RLock()
	types := append([]string(nil), m.cfg.Settings.Notifications.Types...)
	m.cfg.RUnlock()
	if len(types) == 0 {
		return ev.Level == LevelWarn || ev.Level == LevelError
	}
	for _, t := range types {
		if t == ev.Type || strings.HasPrefix(ev.Type, t+".") {
			return true
		}
	}
	return false
}

func (m *Manager) anyConfigured() bool {
	for _, sender := range m.senders() {
		if sender.Configured() {
			return true
		}
	}
	return false
}

func (m *Manager) worker() {
	for ev := range m.queue {
		for _, sender := range m.senders() {
			if !sender.Configured() {
				continue
			}
			if err := sendWithRetry(sender, ev); err != nil {
				// 只写日志中心，避免发送失败事件再次进入通知队列形成递归。
				logcenter.Add("notify", "", "", LevelWarn, fmt.Sprintf("%s 通知发送失败（事件 %s）: %v", sender.Name(), ev.Type, err))
			}
		}
	}
}

func sendWithRetry(sender Sender, ev Event) error {
	var lastErr error
	for attempt := 0; attempt < notificationMaxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(notificationRetryBackoff[attempt-1])
		}
		lastErr = sender.Send(ev)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (m *Manager) Test(channel string) error {
	var sender Sender
	switch channel {
	case "telegram":
		sender = m.telegram
	default:
		return fmt.Errorf("未知的通知渠道: %s", channel)
	}
	if !sender.Configured() {
		return fmt.Errorf("%s 渠道尚未完整配置并启用", sender.Name())
	}
	return sender.Send(Event{
		Type:    TypeTest,
		Level:   LevelInfo,
		Message: "andey-proxy 通知测试：Telegram 渠道配置生效",
		Time:    time.Now(),
	})
}

func (m *Manager) Dropped() int64 {
	m.droppedMu.Lock()
	defer m.droppedMu.Unlock()
	return m.dropped
}
