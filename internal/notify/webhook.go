package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

// webhookQueueCapacity 待发送事件的缓冲容量，满则丢弃新事件（事件本体仍在总线环形缓冲中可查）。
const webhookQueueCapacity = 64

// webhookMaxAttempts 单条事件最大发送次数（首次 + 2 次重试）。
const webhookMaxAttempts = 3

// retryBackoff 重试间隔，测试可注入更短值。
var retryBackoff = []time.Duration{time.Second, 2 * time.Second}

// Webhook 通用 Webhook 订阅者：按配置过滤事件，单 worker 顺序 POST JSON。
type Webhook struct {
	cfg    *config.Config
	client *http.Client
	queue  chan Event

	droppedMu sync.Mutex
	dropped   int64 // 发送队列满被丢弃的事件数
}

// NewWebhook 创建 Webhook 订阅者并启动发送 worker（进程生命周期内常驻）。
func NewWebhook(cfg *config.Config) *Webhook {
	w := &Webhook{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
		queue:  make(chan Event, webhookQueueCapacity),
	}
	go w.worker()
	return w
}

// Handle 总线订阅回调：匹配过滤规则后非阻塞入队，队列满则丢弃并计数。
func (w *Webhook) Handle(ev Event) {
	if !w.match(ev) {
		return
	}
	select {
	case w.queue <- ev:
	default:
		w.droppedMu.Lock()
		w.dropped++
		w.droppedMu.Unlock()
	}
}

// match 判断事件是否需要推送：
// 配置了类型列表时按类型前缀匹配（如 "cert" 匹配 "cert.obtain_failed"）；
// 未配置时默认只推 warn/error 级别。
func (w *Webhook) match(ev Event) bool {
	w.cfg.RLock()
	hookURL := w.cfg.Settings.NotifyWebhookURL
	types := w.cfg.Settings.NotifyTypes
	w.cfg.RUnlock()
	if hookURL == "" {
		return false
	}
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

// worker 单 goroutine 顺序发送，保证事件到达顺序。
func (w *Webhook) worker() {
	for ev := range w.queue {
		w.cfg.RLock()
		hookURL := w.cfg.Settings.NotifyWebhookURL
		w.cfg.RUnlock()
		if hookURL == "" {
			continue
		}
		if err := w.send(hookURL, ev); err != nil {
			// 注意：只能写 logcenter，不能再 Publish 回总线，否则推送失败会递归触发自身。
			logcenter.Add("notify", "", "", LevelWarn, fmt.Sprintf("Webhook 推送失败（事件 %s）: %v", ev.Type, err))
		}
	}
}

// send 向指定 URL POST 事件 JSON，失败按间隔退避重试，最多 webhookMaxAttempts 次。
func (w *Webhook) send(hookURL string, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < webhookMaxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(retryBackoff[attempt-1])
		}
		lastErr = w.post(hookURL, body)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// post 执行一次 POST，非 2xx 视为失败。
func (w *Webhook) post(hookURL string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, hookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("Webhook 地址无效: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("Webhook 返回状态码 %d", resp.StatusCode)
	}
	return nil
}

// Test 同步发送一条测试事件到当前配置的 Webhook，返回发送结果（供 API 使用）。
func (w *Webhook) Test() error {
	w.cfg.RLock()
	hookURL := w.cfg.Settings.NotifyWebhookURL
	w.cfg.RUnlock()
	if hookURL == "" {
		return fmt.Errorf("尚未配置 Webhook 地址")
	}
	return w.send(hookURL, Event{
		Type:    TypeTest,
		Level:   LevelInfo,
		Message: "andey-proxy 通知测试：Webhook 配置生效",
		Time:    time.Now(),
	})
}

// Dropped 返回因发送队列满被丢弃的事件总数。
func (w *Webhook) Dropped() int64 {
	w.droppedMu.Lock()
	defer w.droppedMu.Unlock()
	return w.dropped
}

// ValidateWebhookURL 校验 Webhook 地址：仅 http/https、禁止 userinfo 与片段。
// 空串表示禁用，视为合法。
func ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("必须是有效的 HTTP 或 HTTPS URL")
	}
	if u.User != nil {
		return fmt.Errorf("URL 不能包含用户名或密码")
	}
	if u.Fragment != "" {
		return fmt.Errorf("URL 不能包含片段")
	}
	return nil
}
