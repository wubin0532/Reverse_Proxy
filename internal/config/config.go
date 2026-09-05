package config

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const envelopeVersion = 1

type encryptedEnvelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

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
	IPSource   string   `json:"ipSource"`   // interface / api
	Interface  string   `json:"interface"`  // IPSource=interface 时的网卡名，如 pppoe-wan
	APIURL     string   `json:"apiUrl"`     // IPSource=api 时的查询地址
	Interval   int      `json:"interval"`   // 检测间隔秒
	TTL        int      `json:"ttl"`        // DNS 记录 TTL，0=默认
}

// CertConf 一张 ACME 证书。
type CertConf struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	Domains    []string `json:"domains"` // 支持泛域名 *.example.com
	ProviderID string   `json:"providerId"`
	Email      string   `json:"email"`
	CADirURL   string   `json:"caDirUrl"`  // 空=Let's Encrypt 生产
	RenewDays  int      `json:"renewDays"` // 到期前多少天续签，默认 30
	CertFile   string   `json:"certFile"`  // 相对配置目录
	KeyFile    string   `json:"keyFile"`
	NotAfter   string   `json:"notAfter"` // 最近证书到期时间 RFC3339
	LastError  string   `json:"lastError,omitempty"`
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
	// AutoProxyHeaders 为 nil 时按开启处理，以兼容旧配置。
	AutoProxyHeaders      *bool    `json:"autoProxyHeaders,omitempty"`
	SkipBackendTLSVerify  bool     `json:"skipBackendTlsVerify,omitempty"`
	StripPrefix           bool     `json:"stripPrefix,omitempty"`
	ConnectTimeout        int      `json:"connectTimeoutSeconds,omitempty"`
	ResponseHeaderTimeout int      `json:"responseHeaderTimeoutSeconds,omitempty"`
	RateLimitRPS          int      `json:"rateLimitRPS,omitempty"`
	RateLimitBurst        int      `json:"rateLimitBurst,omitempty"`
	MaxRequestBodyMiB     int      `json:"maxRequestBodyMiB,omitempty"`
	RewriteLocation       bool     `json:"rewriteLocation,omitempty"`
	CookieDomainFrom      string   `json:"cookieDomainFrom,omitempty"`
	CookieDomainTo        string   `json:"cookieDomainTo,omitempty"`
	CookiePathFrom        string   `json:"cookiePathFrom,omitempty"`
	CookiePathTo          string   `json:"cookiePathTo,omitempty"`
	BasicAuth             bool     `json:"basicAuth"`
	AuthUser              string   `json:"authUser"`
	AuthPass              string   `json:"authPass"`
	IPListMode            string   `json:"ipListMode"` // 空 / whitelist / blacklist
	IPList                []string `json:"ipList"`     // IP 或 CIDR
	UAListMode            string   `json:"uaListMode"`
	UAList                []string `json:"uaList"` // User-Agent 关键字
}

// Site 一个 Web 服务站点（监听端口）。
type Site struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Enabled bool      `json:"enabled"`
	Listen  string    `json:"listen"` // 如 :8080
	TLS     bool      `json:"tls"`    // 是否 HTTPS
	CertID  string    `json:"certId"` // 引用 CertConf.ID，空=自签
	AutoFW  bool      `json:"autoFw"` // OpenWrt 下自动放行 WAN 防火墙
	Rules   []SubRule `json:"rules"`
}

// ForwardRule TCP/UDP 端口转发规则。
type ForwardRule struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	Proto      string   `json:"proto"`   // tcp / udp / tcpudp
	Listen     string   `json:"listen"`  // 监听地址 :13389
	Targets    []string `json:"targets"` // 目标地址 ip:port
	AutoFW     bool     `json:"autoFw"`  // OpenWrt 下自动放行 WAN 防火墙
	IPListMode string   `json:"ipListMode"`
	IPList     []string `json:"ipList"`
}

// Settings 全局设置。
type Settings struct {
	AdminUser          string   `json:"adminUser"`
	AdminPassHash      string   `json:"adminPassHash"` // bcrypt
	AdminPort          int      `json:"adminPort"`
	MustChangePassword bool     `json:"mustChangePassword,omitempty"`
	TOTPEnabled        bool     `json:"totpEnabled,omitempty"`
	TOTPSecret         string   `json:"totpSecret,omitempty"`
	TOTPRecoveryHashes []string `json:"totpRecoveryHashes,omitempty"`
}

// Config 根配置。
type Config struct {
	Settings  Settings          `json:"settings"`
	Providers []DNSProviderConf `json:"providers"`
	DDNS      []DDNSTask        `json:"ddns"`
	Certs     []CertConf        `json:"certs"`
	Sites     []Site            `json:"sites"`
	Forwards  []ForwardRule     `json:"forwards"`

	mu        sync.RWMutex
	saveMu    sync.Mutex
	filePath  string
	keyPath   string
	key       []byte
	persisted []byte
}

// Load 从目录加载配置，不存在则创建默认配置。
func Load(dir string) (*Config, error) {
	if filepath.Clean(dir) == string(os.PathSeparator) {
		return nil, errors.New("配置目录不能是文件系统根目录")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
	fp := filepath.Join(dir, "config.json")
	keyPath := dir + ".key"
	c := &Config{filePath: fp, keyPath: keyPath}
	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			key, err := loadOrCreateKey(keyPath)
			if err != nil {
				return nil, err
			}
			c.key = key
			c.Settings = Settings{AdminUser: "admin", AdminPort: 16601, MustChangePassword: true}
			if err := c.Save(); err != nil {
				return nil, err
			}
			return c, nil
		}
		return nil, err
	}
	var envelope encryptedEnvelope
	isEncrypted := json.Unmarshal(data, &envelope) == nil && envelope.Ciphertext != ""
	var key []byte
	if isEncrypted {
		key, err = loadExistingKey(keyPath)
	} else {
		key, err = loadOrCreateKey(keyPath)
	}
	if err != nil {
		return nil, err
	}
	c.key = key
	plain, encrypted, err := decryptIfNeeded(data, key)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(plain, c); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	// 历史版本曾接受但从未实现 webhook IP 来源。迁移时安全禁用，避免
	// 升级后任务持续失败或给用户造成该能力可用的错觉。
	for i := range c.DDNS {
		if c.DDNS[i].IPSource == "webhook" {
			c.DDNS[i].Enabled = false
			c.DDNS[i].IPSource = "interface"
			c.DDNS[i].Interface = "auto"
		}
	}
	_ = os.Chmod(fp, 0o600)
	c.filePath = fp
	c.keyPath = keyPath
	c.key = key
	c.persisted = append([]byte(nil), plain...)
	// 旧明文配置只在成功解析后原子迁移；旧版本已加密配置中的废弃字段
	// 也会通过规范化重写清除，避免已删除模块的敏感值长期残留。
	canonical, marshalErr := json.Marshal(c)
	if marshalErr != nil {
		return nil, marshalErr
	}
	if !encrypted || !bytes.Equal(canonical, plain) {
		if err := c.Save(); err != nil {
			return nil, fmt.Errorf("迁移加密配置失败: %w", err)
		}
	}
	return c, nil
}

// Save 原子写入配置文件。
func (c *Config) Save() error {
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	plain, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := c.persistLocked(plain); err != nil {
		c.rollbackLocked(c.persisted)
		return err
	}
	c.persisted = append(c.persisted[:0], plain...)
	return nil
}

// Update serializes a mutation and its encrypted atomic write. The in-memory
// configuration is restored if validation or persistence fails.
func (c *Config) Update(fn func(*Config) error) error {
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	before, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		c.rollbackLocked(before)
		return err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		c.rollbackLocked(before)
		return err
	}
	if err := c.persistLocked(plain); err != nil {
		c.rollbackLocked(before)
		return err
	}
	c.persisted = append(c.persisted[:0], plain...)
	return nil
}

// persistLocked writes an already marshaled snapshot. The caller holds both
// saveMu and mu so no concurrent request can interleave mutation and commit.
func (c *Config) persistLocked(plain []byte) error {
	data, err := encryptConfig(plain, c.key)
	if err != nil {
		return err
	}
	tmp, err := os.OpenFile(c.filePath+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(c.filePath + ".tmp")
		return err
	}
	if err := os.Rename(c.filePath+".tmp", c.filePath); err != nil {
		_ = os.Remove(c.filePath + ".tmp")
		return err
	}
	if dir, openErr := os.Open(filepath.Dir(c.filePath)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (c *Config) rollbackLocked(snapshot []byte) {
	if len(snapshot) == 0 {
		return
	}
	var stable Config
	if json.Unmarshal(snapshot, &stable) != nil {
		return
	}
	c.Settings, c.Providers, c.DDNS, c.Certs, c.Sites, c.Forwards = stable.Settings, stable.Providers, stable.DDNS, stable.Certs, stable.Sites, stable.Forwards
}

// Dir 返回配置目录。
func (c *Config) Dir() string { return filepath.Dir(c.filePath) }

// RLock / RUnlock / Lock / Unlock 供各模块在读写配置字段时加锁。
func (c *Config) RLock()   { c.mu.RLock() }
func (c *Config) RUnlock() { c.mu.RUnlock() }
func (c *Config) Lock()    { c.mu.Lock() }
func (c *Config) Unlock()  { c.mu.Unlock() }

func (r SubRule) ProxyHeadersEnabled() bool {
	return r.AutoProxyHeaders == nil || *r.AutoProxyHeaders
}

func Bool(v bool) *bool { return &v }

func loadOrCreateKey(path string) ([]byte, error) {
	b, err := loadExistingKey(path)
	if err == nil {
		return b, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return loadOrCreateKey(path)
		}
		return nil, err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return b, nil
}

func loadExistingKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("配置密钥长度无效")
	}
	_ = os.Chmod(path, 0o600)
	return b, nil
}

func encryptConfig(plain, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad := []byte("andey-proxy-config-v1")
	ciphertext := gcm.Seal(nil, nonce, plain, aad)
	env := encryptedEnvelope{Version: envelopeVersion, Algorithm: "AES-256-GCM", Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(ciphertext)}
	return json.MarshalIndent(env, "", "  ")
}

func decryptIfNeeded(data, key []byte) ([]byte, bool, error) {
	var env encryptedEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Ciphertext == "" {
		return data, false, nil
	}
	if env.Version != envelopeVersion || env.Algorithm != "AES-256-GCM" {
		return nil, true, errors.New("不支持的加密配置格式")
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, true, errors.New("配置 nonce 无效")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, true, errors.New("配置密文无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, true, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, true, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte("andey-proxy-config-v1"))
	if err != nil {
		return nil, true, errors.New("配置解密失败：密钥错误或文件已被篡改")
	}
	return plain, true, nil
}
