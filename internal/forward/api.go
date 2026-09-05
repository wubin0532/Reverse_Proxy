package forward

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

var errRuleNotFound = errors.New("规则不存在")

// RegisterRoutes 在已认证的 chi 分组上注册端口转发 CRUD 路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, svc *Service) {
	r.Get("/api/forwards", func(w http.ResponseWriter, _ *http.Request) {
		cfg.RLock()
		rules := append([]config.ForwardRule(nil), cfg.Forwards...)
		cfg.RUnlock()
		type view struct {
			config.ForwardRule
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		result := make([]view, 0, len(rules))
		for _, rule := range rules {
			status, errMsg := svc.RuleStatus(rule.ID)
			if !rule.Enabled {
				status, errMsg = "stopped", ""
			}
			result = append(result, view{ForwardRule: rule, Status: status, Error: errMsg})
		}
		api.OK(w, result)
	})

	r.Post("/api/forwards", func(w http.ResponseWriter, req *http.Request) {
		var rule config.ForwardRule
		if err := api.DecodeBody(req, &rule); err != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		if err := validateRule(&rule); err != nil {
			api.Fail(w, 400, err.Error())
			return
		}
		rule.ID = newID()
		if err := cfg.Update(func(c *config.Config) error {
			c.Forwards = append(c.Forwards, rule)
			return nil
		}); err != nil {
			api.Fail(w, 500, "保存配置失败")
			return
		}
		svc.Reload()
		log.Printf("[security] 新增端口转发规则，ID: %s", rule.ID)
		api.OK(w, nil)
	})

	r.Put("/api/forwards/{id}", func(w http.ResponseWriter, req *http.Request) {
		var body config.ForwardRule
		if err := api.DecodeBody(req, &body); err != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		if err := validateRule(&body); err != nil {
			api.Fail(w, 400, err.Error())
			return
		}
		id := chi.URLParam(req, "id")
		err := cfg.Update(func(c *config.Config) error {
			for i := range c.Forwards {
				if c.Forwards[i].ID == id {
					body.ID = id
					c.Forwards[i] = body
					return nil
				}
			}
			return errRuleNotFound
		})
		if errors.Is(err, errRuleNotFound) {
			api.Fail(w, 404, "规则不存在")
			return
		}
		if err != nil {
			api.Fail(w, 500, "保存配置失败")
			return
		}
		svc.Reload()
		log.Printf("[security] 修改端口转发规则，ID: %s", id)
		api.OK(w, nil)
	})

	r.Delete("/api/forwards/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		err := cfg.Update(func(c *config.Config) error {
			for i := range c.Forwards {
				if c.Forwards[i].ID == id {
					c.Forwards = append(c.Forwards[:i], c.Forwards[i+1:]...)
					return nil
				}
			}
			return errRuleNotFound
		})
		if errors.Is(err, errRuleNotFound) {
			api.Fail(w, 404, "规则不存在")
			return
		}
		if err != nil {
			api.Fail(w, 500, "保存配置失败")
			return
		}
		svc.Reload()
		log.Printf("[security] 删除端口转发规则，ID: %s", id)
		api.OK(w, nil)
	})

	r.Post("/api/forwards/{id}/toggle", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		var enabled bool
		err := cfg.Update(func(c *config.Config) error {
			for i := range c.Forwards {
				if c.Forwards[i].ID == id {
					c.Forwards[i].Enabled = !c.Forwards[i].Enabled
					enabled = c.Forwards[i].Enabled
					return nil
				}
			}
			return errRuleNotFound
		})
		if errors.Is(err, errRuleNotFound) {
			api.Fail(w, 404, "规则不存在")
			return
		}
		if err != nil {
			api.Fail(w, 500, "保存配置失败")
			return
		}
		svc.Reload()
		log.Printf("[security] 切换端口转发规则，ID: %s，启用: %t", id, enabled)
		api.OK(w, nil)
	})

	r.Get("/api/forwards/{id}/logs", func(w http.ResponseWriter, req *http.Request) {
		api.OK(w, svc.Logs(chi.URLParam(req, "id")))
	})
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func validateRule(rule *config.ForwardRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return fmt.Errorf("规则名称不能为空")
	}
	rule.Proto = strings.ToLower(strings.TrimSpace(rule.Proto))
	if rule.Proto == "" {
		rule.Proto = "tcp"
	}
	if rule.Proto != "tcp" && rule.Proto != "udp" && rule.Proto != "tcpudp" {
		return fmt.Errorf("协议必须是 tcp、udp 或 tcpudp")
	}
	listen, err := normalizeListen(rule.Listen)
	if err != nil {
		return fmt.Errorf("监听地址无效: %w", err)
	}
	rule.Listen = listen
	if len(rule.Targets) == 0 || len(rule.Targets) > 32 {
		return fmt.Errorf("目标地址数量必须为 1 到 32 个")
	}
	for i, target := range rule.Targets {
		target = strings.TrimSpace(target)
		host, port, err := net.SplitHostPort(target)
		if err != nil || host == "" || validPort(port) != nil {
			return fmt.Errorf("目标地址无效: %s", target)
		}
		rule.Targets[i] = target
	}
	if err := validateIPList(rule.IPListMode, rule.IPList); err != nil {
		return err
	}
	return nil
}

func normalizeListen(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("不能为空")
	}
	if !strings.Contains(value, ":") {
		value = ":" + value
	}
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", err
	}
	if err := validPort(port); err != nil {
		return "", err
	}
	return value, nil
}

func validPort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("端口必须在 1 到 65535 之间")
	}
	return nil
}

func validateIPList(mode string, list []string) error {
	if mode != "" && mode != "off" && mode != "whitelist" && mode != "blacklist" {
		return fmt.Errorf("IP 名单模式无效")
	}
	if len(list) > 1000 {
		return fmt.Errorf("IP 名单最多 1000 条")
	}
	for _, raw := range list {
		value := strings.TrimSpace(raw)
		if net.ParseIP(value) == nil {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return fmt.Errorf("IP 或 CIDR 无效: %s", raw)
			}
		}
	}
	return nil
}
