package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"andey-proxy/internal/config"
)

var telegramTokenPattern = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{20,}$`)
var telegramChatPattern = regexp.MustCompile(`^(?:-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`)

type TelegramSender struct {
	cfg     *config.Config
	client  *http.Client
	baseURL string
}

func NewTelegramSender(cfg *config.Config) *TelegramSender {
	return &TelegramSender{
		cfg:     cfg,
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://api.telegram.org",
	}
}

func (s *TelegramSender) Name() string { return "Telegram" }

func (s *TelegramSender) settings() config.TelegramNotification {
	s.cfg.RLock()
	defer s.cfg.RUnlock()
	return s.cfg.Settings.Notifications.Telegram
}

func (s *TelegramSender) Configured() bool {
	c := s.settings()
	return c.Enabled && c.BotToken != "" && c.ChatID != ""
}

func (s *TelegramSender) Send(ev Event) error {
	c := s.settings()
	if !c.Enabled || c.BotToken == "" || c.ChatID == "" {
		return errors.New("Telegram 渠道尚未完整配置并启用")
	}
	payload := struct {
		ChatID                string `json:"chat_id"`
		Text                  string `json:"text"`
		MessageThreadID       int64  `json:"message_thread_id,omitempty"`
		DisableWebPagePreview bool   `json:"disable_web_page_preview"`
	}{
		ChatID:                c.ChatID,
		Text:                  formatTelegramMessage(ev),
		MessageThreadID:       c.MessageThreadID,
		DisableWebPagePreview: true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(s.baseURL, "/") + "/bot" + c.BotToken + "/sendMessage"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("创建 Telegram 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		// url.Error 含完整请求 URL，其中包含 Bot Token，必须剥离后再进入日志。
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("连接 Telegram Bot API 失败: %v", err)
	}
	defer resp.Body.Close()
	limited, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(limited, &result)
	if resp.StatusCode/100 != 2 || !result.OK {
		if result.Description == "" {
			result.Description = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("Telegram 返回错误: %s", result.Description)
	}
	return nil
}

func ValidateTelegramConfig(c config.TelegramNotification, tokenRequired bool) error {
	c.BotToken = strings.TrimSpace(c.BotToken)
	c.ChatID = strings.TrimSpace(c.ChatID)
	if c.MessageThreadID < 0 {
		return errors.New("话题 ID 不能小于 0")
	}
	if c.ChatID != "" && !telegramChatPattern.MatchString(c.ChatID) {
		return errors.New("Chat ID 必须是数字或以 @ 开头的频道用户名")
	}
	if c.BotToken != "" && !telegramTokenPattern.MatchString(c.BotToken) {
		return errors.New("Bot Token 格式不正确")
	}
	if c.Enabled {
		if tokenRequired && c.BotToken == "" {
			return errors.New("启用 Telegram 前必须填写 Bot Token")
		}
		if c.ChatID == "" {
			return errors.New("启用 Telegram 前必须填写 Chat ID")
		}
	}
	return nil
}

func formatTelegramMessage(ev Event) string {
	level := map[string]string{LevelInfo: "信息", LevelWarn: "警告", LevelError: "错误"}[ev.Level]
	if level == "" {
		level = ev.Level
	}
	parts := []string{"andey-proxy 通知", "级别：" + level, "类型：" + ev.Type}
	if ev.Entity != "" {
		parts = append(parts, "对象："+ev.Entity)
	}
	if !ev.Time.IsZero() {
		parts = append(parts, "时间："+ev.Time.Local().Format("2006-01-02 15:04:05"))
	}
	parts = append(parts, "内容："+ev.Message)
	message := strings.Join(parts, "\n")
	const maxRunes = 4000
	if utf8.RuneCountInString(message) <= maxRunes {
		return message
	}
	runes := []rune(message)
	return string(runes[:maxRunes-1]) + "…"
}
