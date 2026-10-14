package config

import "fmt"

type WAFConfig struct {
	Enabled       bool `json:"enabled"`
	DetectionOnly bool `json:"detectionOnly"`
	BodyLimitKiB  int  `json:"bodyLimitKiB"`
}
type CCConfig struct {
	Enabled           bool `json:"enabled"`
	WindowSeconds     int  `json:"windowSeconds"`
	MaxRequests       int  `json:"maxRequests"`
	BanSeconds        int  `json:"banSeconds"`
	NotFoundThreshold int  `json:"notFoundThreshold"`
}

func (p *ProtectionPolicy) ValidateExtensions() error {
	if w := p.WAF; w != nil {
		if w.BodyLimitKiB == 0 {
			w.BodyLimitKiB = 256
		}
		if w.BodyLimitKiB < 16 || w.BodyLimitKiB > 1024 {
			return fmt.Errorf("WAF请求体检测上限须为16到1024KiB")
		}
	}
	if c := p.CC; c != nil {
		if c.WindowSeconds == 0 {
			c.WindowSeconds = 60
		}
		if c.WindowSeconds < 1 || c.WindowSeconds > 3600 || c.MaxRequests < 0 || c.MaxRequests > 100000 || c.BanSeconds < 0 || c.BanSeconds > 86400 || c.NotFoundThreshold < 0 || c.NotFoundThreshold > 10000 {
			return fmt.Errorf("CC窗口、请求次数、封禁时间或404阈值无效")
		}
	}
	return nil
}
