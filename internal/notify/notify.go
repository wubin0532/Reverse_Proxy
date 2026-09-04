// Package notify Webhook 通知模块：DDNS 更新、证书申请/续签结果推送。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"andey-proxy/internal/config"
)

// 各推送通道的默认 API 基础地址。
const (
	defaultServerchanBase = "https://sctapi.ftqq.com"
	defaultBarkBase       = "https://api.day.app"
	defaultTelegramBase   = "https://api.telegram.org"
)

// Notifier 按全局设置中的 Webhook 配置推送通知。
type Notifier struct {
	cfg    *config.Config
	client *http.Client

	// 各通道 API 基础地址，测试可覆盖为 httptest.Server
	serverchanBase string
	barkBase       string
	telegramBase   string
}

// New 创建通知器。
func New(cfg *config.Config) *Notifier {
	return &Notifier{
		cfg:            cfg,
		client:         &http.Client{Timeout: 15 * time.Second},
		serverchanBase: defaultServerchanBase,
		barkBase:       defaultBarkBase,
		telegramBase:   defaultTelegramBase,
	}
}

// Notify 事件通知入口，event 为 "ddns" 或 "cert"。
// 仅在启用且订阅了该事件时发送；异步发送，不阻塞调用方。
func (n *Notifier) Notify(event, title, content string) {
	n.cfg.RLock()
	wc := n.cfg.Settings.Webhook
	n.cfg.RUnlock()
	if !wc.Enabled || !hasEvent(wc.Events, event) {
		return
	}
	go func() {
		if err := n.send(wc, title, content); err != nil {
			log.Printf("[通知] 发送失败(type=%s, event=%s): %v", wc.Type, event, err)
		}
	}()
}

func hasEvent(events []string, event string) bool {
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}

// send 同步发送一条消息，成功返回 nil。供 Notify 与测试接口使用。
func (n *Notifier) send(wc config.WebhookConf, title, content string) error {
	switch wc.Type {
	case "serverchan":
		if wc.Key == "" {
			return fmt.Errorf("Server酱 SendKey 为空")
		}
		form := url.Values{"title": {title}, "desp": {content}}
		req, err := http.NewRequest(http.MethodPost,
			fmt.Sprintf("%s/%s.send", n.serverchanBase, wc.Key),
			strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return n.do(req)
	case "bark":
		if wc.Key == "" {
			return fmt.Errorf("Bark Key 为空")
		}
		req, err := postJSON(fmt.Sprintf("%s/%s", n.barkBase, wc.Key),
			map[string]string{"title": title, "body": content})
		if err != nil {
			return err
		}
		return n.do(req)
	case "telegram":
		if wc.Key == "" || wc.ChatID == "" {
			return fmt.Errorf("Telegram Bot Token 或 Chat ID 为空")
		}
		req, err := postJSON(fmt.Sprintf("%s/bot%s/sendMessage", n.telegramBase, wc.Key),
			map[string]string{"chat_id": wc.ChatID, "text": title + "\n" + content})
		if err != nil {
			return err
		}
		return n.do(req)
	case "custom":
		if wc.URL == "" {
			return fmt.Errorf("自定义 Webhook 地址为空")
		}
		req, err := postJSON(wc.URL, map[string]string{"title": title, "content": content})
		if err != nil {
			return err
		}
		return n.do(req)
	}
	return fmt.Errorf("不支持的通知类型: %s", wc.Type)
}

// postJSON 构造一个 JSON POST 请求。
func postJSON(rawURL string, payload interface{}) (*http.Request, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// do 执行请求，非 2xx 视为失败。
func (n *Notifier) do(req *http.Request) error {
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
