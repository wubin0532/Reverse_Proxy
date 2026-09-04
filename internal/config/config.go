package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DNSProviderConf 保存一个 DNS 服务商的凭据，DDNS 与 ACME 共用。
type DNSProviderConf struct {
	ID       string `json:"id"`                 // 内部标识，如 aliyun-1
	Type     string `json:"type"`               // aliyun / cloudflare / dnspod
	Remark   string `json:"remark"`             // 备注
	Key      string `json:"key"`                // AccessKey ID / API Token / Token ID
	Secret   string `json:"secret"`             // AccessKey Secret / API Token Secret（cloudflare 可空）
	Endpoint string `json:"endpoint,omitempty"` // 可选自定义端点
}

// DDNSTask 一条动态域名任务。
type DDNSTask struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	ProviderID string   `json:"providerId"` // 引用 DNSProviderConf.ID
	Domains    []string `json:"domains"`    // 完整域名列表，如 home.example.com
	IPType     string   `json:"ipType"`     // ipv4 / ipv6
	IPSource   string   `json:"ipSource"`   // interface / api / webhook
	Interface  string   `json:"interface"`  // IPSource=interface 时的网卡名，如 pppoe-wan
	APIURL     string   `json:"apiUrl"`     // IPSource=api 时的查询地址
	Interval   int      `json:"interval"`   // 检测间隔秒
	TTL        int      `json:"ttl"`        // DNS 记录 TTL，0=默认
}

// CertConf 一张 ACME 证书。
type CertConf struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Domains      []string `json:"domains"` // 支持泛域名 *.example.com
	ProviderID   string   `json:"providerId"`
	Email        string   `json:"email"`
	CADirURL     string   `json:"caDirUrl"` // 空=Let's Encrypt 生产
	RenewDays    int      `json:"renewDays"` // 到期前多少天续签，默认 30
	CertFile     string   `json:"certFile"`  // 相对配置目录
	KeyFile      string   `json:"keyFile"`
	NotAfter     string   `json:"notAfter"` // 最近证书到期时间 RFC3339
	LastError    string   `json:"lastError,omitempty"`
}

// SubRule Web 服务子规则。
type SubRule struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"` // reverse / redirect / fileserver
	Enabled      bool              `json:"enabled"`
	FrontendHost string            `json:"frontendHost"` // 匹配的前端域名，空=任意
	FrontendPath string            `json:"frontendPath"` // 路径前缀，空=/
	Backends     []string          `json:"backends"`     // reverse 类型：后端地址 http://ip:port
	RedirectURL  string            `json:"redirectUrl"`  // redirect 类型：目标地址
	RedirectCode int               `json:"redirectCode"` // 301/302，默认 302
	RootDir      string            `json:"rootDir"`      // fileserver 类型：目录
	Headers      map[string]string `json:"headers"`      // reverse 附加请求头
	PreserveHost bool              `json:"preserveHost"` // 透传原始 Host
	BasicAuth    bool              `json:"basicAuth"`
	AuthUser     string            `json:"authUser"`
	AuthPass     string            `json:"authPass"`
	IPListMode   string            `json:"ipListMode"` // 空 / whitelist / blacklist
	IPList       []string          `json:"ipList"`     // IP 或 CIDR
	UAListMode   string            `json:"uaListMode"`
	UAList       []string          `json:"uaList"` // User-Agent 关键字
}

// Site 一个 Web 服务站点（监听端口）。
type Site struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Listen    string    `json:"listen"`   // 如 :8080
	TLS       bool      `json:"tls"`      // 是否 HTTPS
	CertID    string    `json:"certId"`   // 引用 CertConf.ID，空=自签
	AutoFW    bool      `json:"autoFw"`   // OpenWrt 下自动放行 WAN 防火墙
	Rules     []SubRule `json:"rules"`
}

// ForwardRule TCP/UDP 端口转发规则。
type ForwardRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Proto     string   `json:"proto"`     // tcp / udp / tcpudp
	Listen    string   `json:"listen"`    // 监听地址 :13389
	Targets   []string `json:"targets"`   // 目标地址 ip:port
	AutoFW    bool     `json:"autoFw"`    // OpenWrt 下自动放行 WAN 防火墙
	IPListMode string  `json:"ipListMode"`
	IPList    []string `json:"ipList"`
}

// WebhookConf 通知配置，用于 DDNS 更新、证书申请/续签结果推送。
type WebhookConf struct {
	Enabled bool     `json:"enabled"`
	Type    string   `json:"type"`   // serverchan / bark / telegram / custom
	Key     string   `json:"key"`    // serverchan SendKey / bark key / telegram bot token
	ChatID  string   `json:"chatId"` // telegram chat_id
	URL     string   `json:"url"`    // custom 类型的完整地址（POST JSON {title,content}）
	Events  []string `json:"events"` // ddns / cert
}

// Settings 全局设置。
type Settings struct {
	AdminUser     string      `json:"adminUser"`
	AdminPassHash string      `json:"adminPassHash"` // bcrypt
	AdminPort     int         `json:"adminPort"`
	Webhook       WebhookConf `json:"webhook"`
}

// Config 根配置。
type Config struct {
	Settings  Settings         `json:"settings"`
	Providers []DNSProviderConf `json:"providers"`
	DDNS      []DDNSTask       `json:"ddns"`
	Certs     []CertConf       `json:"certs"`
	Sites     []Site           `json:"sites"`
	Forwards  []ForwardRule    `json:"forwards"`

	mu       sync.RWMutex
	filePath string
}

// Load 从目录加载配置，不存在则创建默认配置。
func Load(dir string) (*Config, error) {
	fp := filepath.Join(dir, "config.json")
	c := &Config{filePath: fp}
	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			c.Settings = Settings{AdminUser: "666", AdminPort: 16601}
			if err := c.Save(); err != nil {
				return nil, err
			}
			return c, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}
	c.filePath = fp
	return c, nil
}

// Save 原子写入配置文件。
func (c *Config) Save() error {
	c.mu.RLock()
	data, err := json.MarshalIndent(c, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp := c.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.filePath)
}

// Dir 返回配置目录。
func (c *Config) Dir() string { return filepath.Dir(c.filePath) }

// RLock / RUnlock / Lock / Unlock 供各模块在读写配置字段时加锁。
func (c *Config) RLock()   { c.mu.RLock() }
func (c *Config) RUnlock() { c.mu.RUnlock() }
func (c *Config) Lock()    { c.mu.Lock() }
func (c *Config) Unlock()  { c.mu.Unlock() }
