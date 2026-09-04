// Package acme ACME 自动证书模块：DNS-01 申请、定时续签、SNI 证书供给与 API。
package acme

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/registration"

	"andey-proxy/internal/config"
)

// defaultRenewDays RenewDays 未配置时的默认续签提前天数。
const defaultRenewDays = 30

// scanInterval 后台续签扫描间隔。
const scanInterval = 12 * time.Hour

// cachedCert 磁盘证书的内存缓存，modTime 变化时重载。
type cachedCert struct {
	cert    *tls.Certificate
	modTime time.Time
}

// acmeUser 实现 lego 的 registration.User 接口。
type acmeUser struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// Manager 证书申请、续签调度与 SNI 供给。
type Manager struct {
	cfg *config.Config

	// Notify 事件通知回调（可为 nil），申请/续签成功或失败时调用。
	Notify func(event, title, content string)

	mu       sync.RWMutex
	cache    map[string]*cachedCert // key: CertConf.ID
	inflight map[string]bool        // 正在申请中的证书 ID

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewManager 创建证书管理器。
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg:      cfg,
		cache:    make(map[string]*cachedCert),
		inflight: make(map[string]bool),
		stopCh:   make(chan struct{}),
	}
}

// certsDir 证书落盘目录。
func (m *Manager) certsDir() string { return filepath.Join(m.cfg.Dir(), "certs") }

// certPath 返回证书的证书/私钥绝对路径。
func (m *Manager) certPath(c *config.CertConf) (certFile, keyFile string) {
	certFile, keyFile = c.CertFile, c.KeyFile
	if certFile == "" {
		certFile = filepath.Join("certs", c.ID+".crt")
	}
	if keyFile == "" {
		keyFile = filepath.Join("certs", c.ID+".key")
	}
	return filepath.Join(m.cfg.Dir(), certFile), filepath.Join(m.cfg.Dir(), keyFile)
}

// findCert 按 ID 取证书配置快照。
func (m *Manager) findCert(certID string) (config.CertConf, bool) {
	m.cfg.RLock()
	defer m.cfg.RUnlock()
	for _, c := range m.cfg.Certs {
		if c.ID == certID {
			return c, true
		}
	}
	return config.CertConf{}, false
}

// findProvider 按 ID 取服务商凭据快照。
func (m *Manager) findProvider(providerID string) (config.DNSProviderConf, bool) {
	m.cfg.RLock()
	defer m.cfg.RUnlock()
	for _, p := range m.cfg.Providers {
		if p.ID == providerID {
			return p, true
		}
	}
	return config.DNSProviderConf{}, false
}

// newDNSProvider 按凭据类型构造 lego DNS-01 provider（编程式配置，不用环境变量）。
func newDNSProvider(p config.DNSProviderConf) (challenge.Provider, error) {
	switch p.Type {
	case "aliyun":
		c := alidns.NewDefaultConfig()
		c.APIKey = p.Key
		c.SecretKey = p.Secret
		return alidns.NewDNSProviderConfig(c)
	case "cloudflare":
		c := cloudflare.NewDefaultConfig()
		// Secret 非空视为 API Token，否则用 Key
		if p.Secret != "" {
			c.AuthToken = p.Secret
		} else {
			c.AuthToken = p.Key
		}
		return cloudflare.NewDNSProviderConfig(c)
	case "dnspod":
		c := dnspod.NewDefaultConfig()
		c.LoginToken = p.Key + "," + p.Secret
		return dnspod.NewDNSProviderConfig(c)
	}
	return nil, fmt.Errorf("不支持的服务商类型: %s", p.Type)
}

// txtPropagationCheck 生成 DNS-01 传播检查函数：绕过本地 DNS 与权威 NS 直连，
// 只向指定公共递归服务器查询 TXT 值，任一服务器返回期望值即视为已生效。
func txtPropagationCheck(servers []string) dns01.WrapPreCheckFunc {
	return func(_, fqdn, value string, _ dns01.PreCheckFunc) (bool, error) {
		name := strings.TrimSuffix(fqdn, ".")
		deadline := time.Now().Add(2 * time.Minute)
		for {
			for _, srv := range servers {
				resolver := &net.Resolver{
					PreferGo: true,
					Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
						d := net.Dialer{Timeout: 5 * time.Second}
						return d.DialContext(ctx, "udp", srv+":53")
					},
				}
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				txts, err := resolver.LookupTXT(ctx, name)
				cancel()
				if err != nil {
					log.Printf("[acme] 传播检查查询 %s 经 %s 失败: %v", name, srv, err)
					continue
				}
				for _, txt := range txts {
					if txt == value {
						return true, nil
					}
				}
			}
			if time.Now().After(deadline) {
				return false, nil
			}
			time.Sleep(5 * time.Second)
		}
	}
}

// accountKeyPath ACME 账户私钥落盘路径（全局共用一个账户密钥）。
func (m *Manager) accountKeyPath() string {
	return filepath.Join(m.certsDir(), "account.key")
}

// loadAccountKey 读取或生成 ACME 账户私钥。
func (m *Manager) loadAccountKey() (*rsa.PrivateKey, error) {
	fp := m.accountKeyPath()
	data, err := os.ReadFile(fp)
	if err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
				return key, nil
			}
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.certsDir(), 0o700); err != nil {
		return nil, err
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(fp, pemData, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// parseNotAfter 从 PEM 证书（可为 bundle，取第一张）解析到期时间。
func parseNotAfter(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, errors.New("证书 PEM 格式不正确")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}

// renewDaysOf 取生效的续签提前天数。
func renewDaysOf(c *config.CertConf) int {
	if c.RenewDays > 0 {
		return c.RenewDays
	}
	return defaultRenewDays
}

// needRenew 判断是否需要续签：NotAfter 缺失、解析失败或距到期 <= RenewDays。
func needRenew(c *config.CertConf, now time.Time) bool {
	if c.NotAfter == "" {
		return true
	}
	notAfter, err := time.Parse(time.RFC3339, c.NotAfter)
	if err != nil {
		return true
	}
	return !notAfter.After(now.Add(time.Duration(renewDaysOf(c)) * 24 * time.Hour))
}

// beginObtain 标记证书进入申请中，已在申请则返回 false。
func (m *Manager) beginObtain(certID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inflight[certID] {
		return false
	}
	m.inflight[certID] = true
	return true
}

// endObtain 清除申请中标记。
func (m *Manager) endObtain(certID string) {
	m.mu.Lock()
	delete(m.inflight, certID)
	m.mu.Unlock()
}

// Obtaining 证书是否正在申请中。
func (m *Manager) Obtaining(certID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inflight[certID]
}

// invalidate 使指定证书的磁盘缓存失效。
func (m *Manager) invalidate(certID string) {
	m.mu.Lock()
	delete(m.cache, certID)
	m.mu.Unlock()
}

// setResult 申请结束后回写 CertConf 的证书路径、到期时间与错误信息。
func (m *Manager) setResult(certID, notAfter, lastErr string) {
	m.cfg.Lock()
	for i := range m.cfg.Certs {
		if m.cfg.Certs[i].ID == certID {
			c := &m.cfg.Certs[i]
			if lastErr == "" {
				c.CertFile = filepath.Join("certs", certID+".crt")
				c.KeyFile = filepath.Join("certs", certID+".key")
				c.NotAfter = notAfter
			}
			c.LastError = lastErr
			break
		}
	}
	m.cfg.Unlock()
	if err := m.cfg.Save(); err != nil {
		log.Printf("[acme] 保存配置失败: %v", err)
	}
}

// Obtain 申请或强制重签一张证书（DNS-01）。同一证书并发申请会被拒绝。
func (m *Manager) Obtain(ctx context.Context, certID string) error {
	if !m.beginObtain(certID) {
		return errors.New("该证书正在申请中")
	}
	defer m.endObtain(certID)

	notAfter, err := m.obtain(ctx, certID)
	if err != nil {
		m.setResult(certID, "", err.Error())
		m.notifyResult(certID, false, "", err)
		return err
	}
	m.setResult(certID, notAfter, "")
	m.invalidate(certID)
	m.notifyResult(certID, true, notAfter, nil)
	return nil
}

// notifyResult 申请/续签结束后推送结果通知（未设置回调时跳过）。
func (m *Manager) notifyResult(certID string, success bool, notAfter string, err error) {
	if m.Notify == nil {
		return
	}
	cert, _ := m.findCert(certID)
	domains := strings.Join(cert.Domains, ", ")
	if success {
		m.Notify("cert", "andey-Proxy 证书申请成功",
			fmt.Sprintf("证书: %s\n域名: %s\n到期时间: %s", cert.Name, domains, notAfter))
		return
	}
	m.Notify("cert", "andey-Proxy 证书申请失败",
		fmt.Sprintf("证书: %s\n域名: %s\n错误: %s", cert.Name, domains, err.Error()))
}

// obtain 执行一次完整的申请流程，成功返回新证书到期时间（RFC3339）。
func (m *Manager) obtain(ctx context.Context, certID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cert, ok := m.findCert(certID)
	if !ok {
		return "", fmt.Errorf("证书不存在: %s", certID)
	}
	if len(cert.Domains) == 0 {
		return "", errors.New("域名列表不能为空")
	}
	provider, ok := m.findProvider(cert.ProviderID)
	if !ok {
		return "", fmt.Errorf("服务商凭据不存在: %s", cert.ProviderID)
	}
	dnsProvider, err := newDNSProvider(provider)
	if err != nil {
		return "", err
	}
	accountKey, err := m.loadAccountKey()
	if err != nil {
		return "", fmt.Errorf("加载账户私钥失败: %w", err)
	}

	user := &acmeUser{email: cert.Email, key: accountKey}
	lcfg := lego.NewConfig(user)
	if cert.CADirURL != "" {
		lcfg.CADirURL = cert.CADirURL // 空 = Let's Encrypt 生产目录
	}
	client, err := lego.NewClient(lcfg)
	if err != nil {
		return "", err
	}
	// 路由本地 DNS 常被代理软件劫持（fake-ip/NXDOMAIN），且国内网络无法直连
	// 境外权威 NS（UDP 53 超时），国内递归 DNS 又存在 TTL 缓存不刷新的问题。
	// zone 探测走公共递归 DNS；传播检查改为自定义逻辑：仅向遵守 TTL 的公共
	// 递归服务器校验 TXT 值，轮询直至出现或超时。
	challengeOpts := []dns01.ChallengeOption{
		dns01.AddRecursiveNameservers(dns01.ParseNameservers([]string{"223.5.5.5", "119.29.29.29", "1.1.1.1"})),
		dns01.WrapPreCheck(txtPropagationCheck([]string{"1.1.1.1", "8.8.8.8"})),
	}
	if err := client.Challenge.SetDNS01Provider(dnsProvider, challengeOpts...); err != nil {
		return "", err
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return "", fmt.Errorf("注册 ACME 账户失败: %w", err)
	}
	user.reg = reg

	log.Printf("[acme] 开始申请证书 %s，域名: %s", cert.Name, strings.Join(cert.Domains, ", "))
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: cert.Domains, Bundle: true})
	if err != nil {
		return "", err
	}
	notAfter, err := parseNotAfter(res.Certificate)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(m.certsDir(), 0o700); err != nil {
		return "", err
	}
	certFile, keyFile := m.certPath(&cert)
	if err := os.WriteFile(certFile, res.Certificate, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(keyFile, res.PrivateKey, 0o600); err != nil {
		return "", err
	}
	log.Printf("[acme] 证书 %s 申请成功，到期时间 %s", cert.Name, notAfter.Format(time.RFC3339))
	return notAfter.Format(time.RFC3339), nil
}

// Start 启动后台续签循环：立即扫描一次，之后每 12 小时扫描。
func (m *Manager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.scan()
		ticker := time.NewTicker(scanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.scan()
			}
		}
	}()
}

// Stop 停止后台续签循环并等待退出。
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// scan 扫描所有启用的证书，缺失或临近到期的自动申请/续签。
func (m *Manager) scan() {
	m.cfg.RLock()
	certs := make([]config.CertConf, len(m.cfg.Certs))
	copy(certs, m.cfg.Certs)
	m.cfg.RUnlock()

	now := time.Now()
	for i := range certs {
		c := &certs[i]
		if !c.Enabled {
			continue
		}
		certFile, keyFile := m.certPath(c)
		_, certErr := os.Stat(certFile)
		_, keyErr := os.Stat(keyFile)
		missing := certErr != nil || keyErr != nil
		if !missing && !needRenew(c, now) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if err := m.Obtain(ctx, c.ID); err != nil {
			log.Printf("[acme] 证书 %s 申请/续签失败: %v", c.Name, err)
		}
		cancel()
	}
}

// matchDomain 判断域名 pattern（支持 *.example.com 泛域名，仅匹配一级子域）是否匹配 name。
func matchDomain(pattern, name string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
	if pattern == name {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if strings.HasSuffix(name, suffix) {
			// 前缀部分不含点，只匹配一级子域
			return !strings.Contains(name[:len(name)-len(suffix)], ".")
		}
	}
	return false
}

// matchCert 按 SNI 域名找已启用证书，精确匹配优先于泛域名。
func (m *Manager) matchCert(name string) (config.CertConf, bool) {
	m.cfg.RLock()
	certs := make([]config.CertConf, len(m.cfg.Certs))
	copy(certs, m.cfg.Certs)
	m.cfg.RUnlock()

	var wildcard *config.CertConf
	for i := range certs {
		c := &certs[i]
		if !c.Enabled {
			continue
		}
		for _, d := range c.Domains {
			d = strings.TrimSpace(d)
			if strings.EqualFold(strings.TrimSuffix(d, "."), name) {
				return *c, true // 精确匹配优先
			}
			if wildcard == nil && matchDomain(d, name) {
				wildcard = c
			}
		}
	}
	if wildcard != nil {
		return *wildcard, true
	}
	return config.CertConf{}, false
}

// GetCertificate 供 tls.Config.GetCertificate 使用，按 SNI 返回证书。
// 从磁盘加载并缓存，证书文件 mtime 变化时自动重载。
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if name == "" {
		return nil, fmt.Errorf("客户端未提供 SNI: %w", os.ErrNotExist)
	}
	cert, ok := m.matchCert(name)
	if !ok {
		return nil, fmt.Errorf("没有匹配域名 %s 的证书: %w", name, os.ErrNotExist)
	}
	return m.loadCached(&cert)
}

// loadCached 加载证书文件，缓存命中且 mtime 未变时直接返回缓存。
func (m *Manager) loadCached(c *config.CertConf) (*tls.Certificate, error) {
	certFile, keyFile := m.certPath(c)
	fi, err := os.Stat(certFile)
	if err != nil {
		return nil, fmt.Errorf("证书文件不可用: %w", err)
	}
	m.mu.RLock()
	cached := m.cache[c.ID]
	m.mu.RUnlock()
	if cached != nil && cached.modTime.Equal(fi.ModTime()) {
		return cached.cert, nil
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[c.ID] = &cachedCert{cert: &pair, modTime: fi.ModTime()}
	m.mu.Unlock()
	return &pair, nil
}

// RemoveFiles 删除证书对应的磁盘文件并清空缓存（删除配置时调用）。
func (m *Manager) RemoveFiles(c *config.CertConf) {
	certFile, keyFile := m.certPath(c)
	os.Remove(certFile)
	os.Remove(keyFile)
	m.invalidate(c.ID)
}
