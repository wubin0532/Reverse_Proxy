package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HealthCheckConf is user configuration, encrypted and backed up with Config.
type HealthCheckConf struct {
	Enabled         bool   `json:"enabled"`
	Type            string `json:"type"`
	Path            string `json:"path"`
	IntervalSeconds int    `json:"intervalSeconds"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	Rise            int    `json:"rise"`
	Fall            int    `json:"fall"`
}

func (c HealthCheckConf) WithDefaults() HealthCheckConf {
	if c.Type == "" {
		c.Type = "tcp"
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 10
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 3
	}
	if c.Rise == 0 {
		c.Rise = 1
	}
	if c.Fall == 0 {
		c.Fall = 2
	}
	return c
}

func (c *HealthCheckConf) Validate() error {
	*c = c.WithDefaults()
	if c.Type != "tcp" && c.Type != "http" {
		return fmt.Errorf("健康检查类型必须是 tcp 或 http")
	}
	if !strings.HasPrefix(c.Path, "/") || strings.ContainsAny(c.Path, " \r\n\t") {
		return fmt.Errorf("健康检查路径必须以 / 开头且不含空白字符")
	}
	if c.Type == "tcp" {
		c.Path = "/"
	}
	if c.IntervalSeconds < 2 || c.IntervalSeconds > 300 {
		return fmt.Errorf("健康检查间隔必须为 2 到 300 秒")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 60 {
		return fmt.Errorf("健康检查超时必须为 1 到 60 秒")
	}
	if c.TimeoutSeconds > c.IntervalSeconds {
		return fmt.Errorf("健康检查超时不能大于探测间隔")
	}
	if c.Rise < 1 || c.Rise > 10 {
		return fmt.Errorf("恢复阈值必须为 1 到 10 次")
	}
	if c.Fall < 1 || c.Fall > 10 {
		return fmt.Errorf("摘除阈值必须为 1 到 10 次")
	}
	return nil
}

func (c *Config) normalizeRuleHealth() error {
	for id, conf := range c.RuleHealthChecks {
		if err := conf.Validate(); err != nil {
			return err
		}
		c.RuleHealthChecks[id] = conf
	}
	return nil
}

// Only migrate legacy sidecars when the main config predates this field.
// A restored older backup intentionally clears checks and must not resurrect it.
func (c *Config) migrateRuleHealth(plain []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(plain, &fields); err != nil {
		return err
	}
	if _, present := fields["ruleHealthChecks"]; !present {
		data, err := os.ReadFile(filepath.Join(c.Dir(), "webproxy-health.json"))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("读取旧健康检查配置失败: %w", err)
		}
		if err == nil {
			var legacy struct {
				Rules map[string]HealthCheckConf `json:"rules"`
			}
			if err := json.Unmarshal(data, &legacy); err != nil {
				return fmt.Errorf("解析旧健康检查配置失败: %w", err)
			}
			c.RuleHealthChecks = legacy.Rules
		}
	}
	return c.normalizeRuleHealth()
}
