// Package acme ACME 自动证书模块：DNS-01 申请、定时续签、SNI 证书供给与 API。
package acme

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/registration"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/notify"
)

// defaultRenewDays RenewDays 未配置时的默认续签提前天数。
const defaultRenewDays = 30

// scanInterval 后台续签扫描间隔。
const scanInterval = 12 * time.Hour

// cachedCert holds one validated pair; the fingerprint includes both files.
// certStamp/keyStamp 记录两份文件的轻量标识，使 TLS 握手热路径不必每次读盘。
type cachedCert struct {
	cert        *tls.Certificate
	fingerprint [32]byte
	certStamp   fileStamp
	keyStamp    fileStamp
}

// fileStamp 文件大小与修改时间，用于判断缓存是否仍然有效。
type fileStamp struct{ size, mtime int64 }

func stampOf(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}, nil
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

	mu       sync.RWMutex
	cache    map[string]*cachedCert // key: CertConf.ID
	inflight map[string]bool        // 正在申请中的证书 ID

	// accountMu 串行化账户私钥首建（loadAccountKey），独立于 m.mu，
	// 避免 RSA 密钥生成耗时阻塞证书缓存读写。
	accountMu sync.Mutex

	// pairMu excludes readers while both certificate files are being replaced.
	// Where both locks are needed, acquire cfg before pairMu.
	pairMu         sync.RWMutex
	mappingMu      sync.Mutex
	mappingResults sync.Map

	ctx      context.Context // Stop 时取消，中断正在进行的申请
	cancel   context.CancelFunc
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	// reloadMu 保护 reloadRunning/reloadPending/stopped，串行化并去抖 Reload 调用。
	reloadMu      sync.Mutex
	reloadRunning bool
	reloadPending bool
	started       bool
	stopped       bool // Stop 已发起，禁止再起 Reload goroutine（保证 wg.Add 先于 wg.Wait）
}

// NewManager 创建证书管理器。
func NewManager(cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:      cfg,
		cache:    make(map[string]*cachedCert),
		inflight: make(map[string]bool),
		ctx:      ctx,
		cancel:   cancel,
		stopCh:   make(chan struct{}),
	}
}

// certsDir 证书落盘目录。
func (m *Manager) certsDir() string { return filepath.Join(m.cfg.Dir(), "certs") }

// certPath 返回证书的证书/私钥绝对路径。
func (m *Manager) certPath(c *config.CertConf) (certFile, keyFile string) {
	// 证书路径只由内部 ID 决定，不信任历史配置中的路径字段，避免目录穿越。
	base := filepath.Base(c.ID)
	return filepath.Join(m.certsDir(), base+".crt"), filepath.Join(m.certsDir(), base+".key")
}

// findCert 按 ID 取证书配置快照。
func (m *Manager) findCert(certID string) (config.CertConf, bool) {
	m.cfg.RLock()
	defer m.cfg.RUnlock()
	for _, c := range m.cfg.Certs {
		if c.ID == certID {
			c.Domains = append([]string(nil), c.Domains...)
			c.DNSResolvers = append([]string(nil), c.DNSResolvers...)
			c.Mappings = append([]config.CertificateMapping(nil), c.Mappings...)
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

// accountKeyPath ACME 账户私钥落盘路径（全局共用一个账户密钥）。
func (m *Manager) accountKeyPath() string {
	return filepath.Join(m.certsDir(), "account.key")
}

// parseAccountKey 解析 PEM 编码的 ACME 账户私钥。
func parseAccountKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block != nil {
		if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return key, nil
		}
	}
	return nil, errors.New("ACME 账户私钥损坏，拒绝自动覆盖")
}

// loadAccountKey 读取或生成 ACME 账户私钥。
func (m *Manager) loadAccountKey() (*rsa.PrivateKey, error) {
	fp := m.accountKeyPath()
	data, err := os.ReadFile(fp)
	if err == nil {
		return parseAccountKey(data)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	// 首次创建须串行化：并发首申时若各自生成密钥并 rename，
	// 后写覆盖先写，两个 goroutine 会注册出不同的 ACME 账户。
	m.accountMu.Lock()
	defer m.accountMu.Unlock()
	// 双检：拿锁后文件可能已被并发调用方创建
	data, err = os.ReadFile(fp)
	if err == nil {
		return parseAccountKey(data)
	}
	if !os.IsNotExist(err) {
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
	tmp, err := writeTempFile(m.certsDir(), ".account-key-*", pemData)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, fp); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if dir, openErr := os.Open(m.certsDir()); openErr == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	if err != nil {
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
func needRenew(c *config.CertConf, st config.CertState, now time.Time) bool {
	if st.NotAfter == "" {
		return true
	}
	notAfter, err := time.Parse(time.RFC3339, st.NotAfter)
	if err != nil {
		return true
	}
	return !notAfter.After(now.Add(time.Duration(renewDaysOf(c)) * 24 * time.Hour))
}

// beginObtain 标记证书进入申请中，已在申请则返回 false。
func (m *Manager) beginObtain(certID string) bool {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if m.stopped {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inflight[certID] {
		return false
	}
	m.inflight[certID] = true
	m.wg.Add(1)
	return true
}

// endObtain 清除申请中标记。
func (m *Manager) endObtain(certID string) {
	m.mu.Lock()
	delete(m.inflight, certID)
	m.mu.Unlock()
	m.wg.Done()
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

func (m *Manager) writeResult(certID, notAfter, lastErr string) {
	st := m.cfg.State()
	st.Update(func(s *config.State) {
		cs := s.Certs[certID]
		if lastErr == "" {
			cs.CertFile = filepath.Join("certs", certID+".crt")
			cs.KeyFile = filepath.Join("certs", certID+".key")
			cs.NotAfter = notAfter
		}
		cs.LastError = lastErr
		s.Certs[certID] = cs
	})
	if err := st.Flush(); err != nil {
		log.Printf("[acme] 保存运行状态失败: %v", err)
	}
}

// Obtain 申请或强制重签一张证书（DNS-01）。同一证书并发申请会被拒绝。
func (m *Manager) Obtain(ctx context.Context, certID string) error {
	if !m.beginObtain(certID) {
		return errors.New("该证书正在申请中")
	}
	defer m.endObtain(certID)
	return m.runObtain(ctx, certID)
}

func (m *Manager) runObtain(parent context.Context, certID string) error {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(m.ctx, cancel)
	defer func() { stop(); cancel() }()
	cert, ok := m.findCert(certID)
	if !ok {
		return fmt.Errorf("证书不存在: %s", certID)
	}
	if cert.Source == "uploaded" {
		return errors.New("导入证书不支持 ACME 申请，请导入新证书替换")
	}
	provider, ok := m.findProvider(cert.ProviderID)
	if !ok {
		err := fmt.Errorf("服务商凭据不存在: %s", cert.ProviderID)
		m.setRequestError(cert, config.DNSProviderConf{}, err.Error())
		return err
	}
	notAfter, err := m.obtain(ctx, cert, provider)
	name := certID
	if c, ok := m.findCert(certID); ok && c.Name != "" {
		name = c.Name
	}
	if err != nil {
		if m.ctx.Err() != nil || errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) || errors.Is(err, errRequestChanged) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		safeErr := sanitizeProviderError(provider, err)
		for _, secret := range []string{cert.EABHMAC, cert.EABKeyID} {
			if secret != "" {
				safeErr = errors.New(strings.ReplaceAll(safeErr.Error(), secret, "[REDACTED]"))
			}
		}
		m.setRequestError(cert, provider, safeErr.Error())
		notify.Publish(notify.Event{Type: notify.TypeCertObtainFailed, Entity: name, Level: notify.LevelError, Message: fmt.Sprintf("证书 %s 申请/续签失败: %v", name, safeErr)})
		return safeErr
	}
	m.autoSyncMappings(certID)
	notify.Publish(notify.Event{Type: notify.TypeCertObtainSuccess, Entity: name, Level: notify.LevelInfo, Message: fmt.Sprintf("证书 %s 申请/续签成功，到期时间 %s", name, notAfter)})
	return nil
}

func (m *Manager) sanitizeError(certID string, err error) error {
	var provider config.DNSProviderConf
	if cert, ok := m.findCert(certID); ok {
		provider, _ = m.findProvider(cert.ProviderID)
	}
	return sanitizeProviderError(provider, err)
}

func sanitizeProviderError(provider config.DNSProviderConf, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range []string{provider.Key, provider.Secret} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return errors.New(logcenter.Redact(message))
}

// obtain 执行一次完整的申请流程，成功返回新证书到期时间（RFC3339）。
// lego DNS resolver options use package-global state; serialize the complete
// DNS operation across managers so concurrent certificates cannot exchange NS.
var dnsOperations = make(chan struct{}, 1)

func (m *Manager) obtain(ctx context.Context, cert config.CertConf, provider config.DNSProviderConf) (string, error) {
	select {
	case dnsOperations <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-dnsOperations }()

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(cert.Domains) == 0 {
		return "", errors.New("域名列表不能为空")
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
	if cert.CATrustPEM != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		roots.AppendCertsFromPEM([]byte(cert.CATrustPEM))
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
		lcfg.HTTPClient = &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	lcfg.HTTPClient = operationHTTPClient(ctx, lcfg.HTTPClient)
	if cert.KeyType != "" {
		kt, err := certificateKeyType(cert.KeyType)
		if err != nil {
			return "", err
		}
		lcfg.Certificate.KeyType = kt
	}
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
	resolvers := append([]string(nil), cert.DNSResolvers...)
	if len(resolvers) == 0 {
		resolvers = []string{"223.5.5.5", "119.29.29.29", "1.1.1.1"}
	}
	timeout := cert.PropagationTimeout
	if timeout == 0 {
		timeout = 120
	}
	interval := cert.PropagationInterval
	if interval == 0 {
		interval = 5
	}
	dnsProvider = timedDNSProvider{Provider: dnsProvider, timeout: time.Duration(timeout) * time.Second, interval: time.Duration(interval) * time.Second}
	challengeOpts := []dns01.ChallengeOption{dns01.AddRecursiveNameservers(dns01.ParseNameservers(resolvers)), dns01.WrapPreCheck(txtPropagationCheckOptions(ctx, resolvers, time.Duration(timeout)*time.Second, time.Duration(interval)*time.Second))}

	if err := client.Challenge.SetDNS01Provider(dnsProvider, challengeOpts...); err != nil {
		return "", err
	}
	var reg *registration.Resource
	if cert.EABEnabled {
		reg, err = client.Registration.RegisterWithExternalAccountBinding(registration.RegisterEABOptions{TermsOfServiceAgreed: true, Kid: cert.EABKeyID, HmacEncoded: strings.TrimRight(cert.EABHMAC, "=")})
	} else {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	}
	if err != nil {
		return "", fmt.Errorf("注册 ACME 账户失败: %w", err)
	}
	user.reg = reg

	log.Printf("[acme] 开始申请证书 %s，域名: %s", cert.Name, strings.Join(cert.Domains, ", "))
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: cert.Domains, Bundle: true})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(m.certsDir(), 0o700); err != nil {
		return "", err
	}
	notAfter, err := m.commitCertificate(ctx, cert, provider, res.Certificate, res.PrivateKey)
	if err != nil {
		return "", err
	}
	log.Printf("[acme] 证书 %s 申请成功，到期时间 %s", cert.Name, notAfter)
	return notAfter, nil
}

// writeCertificatePair validates and stages both files before replacing the
// active pair. A failure restores the previous pair instead of leaving a new
// certificate with an old key (or a truncated private key).
func writeCertificatePair(certFile, keyFile string, certPEM, keyPEM []byte) error {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("证书与私钥不匹配: %w", err)
	}
	dir := filepath.Dir(certFile)
	certTmp, err := writeTempFile(dir, ".cert-*", certPEM)
	if err != nil {
		return err
	}
	defer os.Remove(certTmp)
	keyTmp, err := writeTempFile(dir, ".key-*", keyPEM)
	if err != nil {
		return err
	}
	defer os.Remove(keyTmp)

	certBackup, keyBackup := certFile+".previous", keyFile+".previous"
	_ = os.Remove(certBackup)
	_ = os.Remove(keyBackup)
	hadCert, err := moveIfExists(certFile, certBackup)
	if err != nil {
		return err
	}
	hadKey, err := moveIfExists(keyFile, keyBackup)
	if err != nil {
		if hadCert {
			_ = os.Rename(certBackup, certFile)
		}
		return err
	}
	restore := func() {
		_ = os.Remove(certFile)
		_ = os.Remove(keyFile)
		if hadCert {
			_ = os.Rename(certBackup, certFile)
		}
		if hadKey {
			_ = os.Rename(keyBackup, keyFile)
		}
	}
	if err := os.Rename(keyTmp, keyFile); err != nil {
		restore()
		return err
	}
	if err := os.Rename(certTmp, certFile); err != nil {
		restore()
		return err
	}
	if handle, openErr := os.Open(dir); openErr == nil {
		err = handle.Sync()
		_ = handle.Close()
	}
	if err != nil {
		restore()
		return err
	}
	_ = os.Remove(certBackup)
	_ = os.Remove(keyBackup)
	return nil
}

func writeTempFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	_ = f.Chmod(0o600)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func moveIfExists(from, to string) (bool, error) {
	if err := os.Rename(from, to); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Start 启动后台续签循环：立即扫描一次，之后每 12 小时扫描。
func (m *Manager) Start() {
	m.reloadMu.Lock()
	if m.stopped || m.started {
		m.reloadMu.Unlock()
		return
	}
	m.started = true
	m.wg.Add(1)
	m.reloadMu.Unlock()
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

// Reload 立即按当前配置重新协调：清理已删除/禁用证书的缓存，
// 申请缺失或临期的证书。可并发调用：进行中的协调会合并后续调用，
// 结束后至多再跑一次，不会并发重叠。
func (m *Manager) Reload() {
	m.reloadMu.Lock()
	if m.stopped {
		m.reloadMu.Unlock()
		return
	}
	if m.reloadRunning {
		m.reloadPending = true
		m.reloadMu.Unlock()
		return
	}
	m.reloadRunning = true
	m.wg.Add(1) // 在 reloadMu 内 Add，与 Stop 的 Wait 形成顺序，避免竞态
	m.reloadMu.Unlock()
	go func() {
		defer m.wg.Done()
		for {
			m.reconcile()
			m.reloadMu.Lock()
			if !m.reloadPending || m.ctx.Err() != nil {
				m.reloadRunning = false
				m.reloadPending = false
				m.reloadMu.Unlock()
				return
			}
			m.reloadPending = false
			m.reloadMu.Unlock()
		}
	}()
}

// reconcile 一次完整协调：先剪除失效缓存，再扫描申请。
func (m *Manager) reconcile() {
	m.pruneCache()
	m.scan()
}

// pruneCache 丢弃已删除或已禁用证书的缓存。
func (m *Manager) pruneCache() {
	m.cfg.RLock()
	active := make(map[string]bool, len(m.cfg.Certs))
	for _, c := range m.cfg.Certs {
		if c.Enabled {
			active[c.ID] = true
		}
	}
	m.cfg.RUnlock()
	m.mu.Lock()
	for id := range m.cache {
		if !active[id] {
			delete(m.cache, id)
		}
	}
	m.mu.Unlock()
}

// Stop 停止后台续签循环并等待退出；先取消进行中的申请，可重复调用。
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		m.reloadMu.Lock()
		m.stopped = true
		m.reloadMu.Unlock()
		m.cancel()
		close(m.stopCh)
		m.wg.Wait()
	})
}

// scan 扫描所有启用的证书，缺失或临近到期的自动申请/续签。
func (m *Manager) scan() {
	m.cfg.RLock()
	certs := make([]config.CertConf, len(m.cfg.Certs))
	copy(certs, m.cfg.Certs)
	m.cfg.RUnlock()

	now := time.Now()
	for i := range certs {
		if m.ctx.Err() != nil { // Stop 后不再申请后续证书
			return
		}
		c := &certs[i]
		if !c.Enabled || c.Source == "uploaded" {
			continue
		}
		certFile, keyFile := m.certPath(c)
		_, certErr := os.Stat(certFile)
		_, keyErr := os.Stat(keyFile)
		missing := certErr != nil || keyErr != nil
		if !missing {
			pair, err := m.loadCached(c)
			missing = err != nil || !coversDomains(pair, c.Domains)
		}
		if !missing && !needRenew(c, m.cfg.State().Cert(c.ID), now) {
			continue
		}
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Minute)
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
// 从磁盘加载并缓存，证书或私钥内容变化时重新校验并重载。
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if name == "" {
		return nil, fmt.Errorf("客户端未提供 SNI: %w", os.ErrNotExist)
	}
	cert, ok := m.matchCert(name)
	if !ok {
		return nil, fmt.Errorf("没有匹配域名 %s 的证书: %w", name, os.ErrNotExist)
	}
	pair, err := m.loadCached(&cert)
	if err != nil {
		return nil, err
	}
	leaf, err := certificateLeaf(pair)
	if err != nil {
		return nil, err
	}
	if err := leaf.VerifyHostname(name); err != nil {
		return nil, fmt.Errorf("证书尚未覆盖域名 %s: %w", name, os.ErrNotExist)
	}
	return pair, nil
}

// loadCached fingerprints both files under the shared publication lock.
func (m *Manager) loadCached(c *config.CertConf) (*tls.Certificate, error) {
	m.pairMu.RLock()
	defer m.pairMu.RUnlock()
	certFile, keyFile := m.certPath(c)
	certStamp, err := stampOf(certFile)
	if err != nil {
		return nil, fmt.Errorf("证书文件不可用: %w", err)
	}
	keyStamp, err := stampOf(keyFile)
	if err != nil {
		return nil, fmt.Errorf("私钥文件不可用: %w", err)
	}
	// 在锁内复制出需要的字段：storeStamps 会就地更新这些标识，若在锁外读取
	// 就会与并发的 storeStamps 构成数据竞争（cert 指针本身不可变，可在锁外用）。
	m.mu.RLock()
	cached := m.cache[c.ID]
	var cachedFingerprint [32]byte
	var cachedCertStamp, cachedKeyStamp fileStamp
	if cached != nil {
		cachedFingerprint = cached.fingerprint
		cachedCertStamp, cachedKeyStamp = cached.certStamp, cached.keyStamp
	}
	m.mu.RUnlock()
	// 快路径：两份文件的大小与修改时间都没变，直接复用已解析的证书。
	// 原先每次 TLS 握手都要 os.ReadFile 证书与私钥、再算一次 SHA-256，
	// 使指纹缓存形同虚设（必须先读盘才能命中）。
	if cached != nil && cachedCertStamp == certStamp && cachedKeyStamp == keyStamp {
		return cached.cert, nil
	}
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("证书文件不可用: %w", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("私钥文件不可用: %w", err)
	}
	fingerprint := pairFingerprint(certPEM, keyPEM)
	if cached != nil && cachedFingerprint == fingerprint {
		// 内容未变（例如仅被 touch）：刷新标识后继续复用。
		m.storeStamps(c.ID, certStamp, keyStamp)
		return cached.cert, nil
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	m.publishPair(c.ID, &pair, certPEM, keyPEM)
	m.storeStamps(c.ID, certStamp, keyStamp)
	return &pair, nil
}

// storeStamps 为缓存条目记录对应的文件标识，使后续握手可走 stat 快路径。
func (m *Manager) storeStamps(id string, certStamp, keyStamp fileStamp) {
	m.mu.Lock()
	if entry := m.cache[id]; entry != nil {
		entry.certStamp, entry.keyStamp = certStamp, keyStamp
	}
	m.mu.Unlock()
}

func pairFingerprint(certPEM, keyPEM []byte) [32]byte {
	h := sha256.New()
	h.Write(certPEM)
	h.Write([]byte{0})
	h.Write(keyPEM)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// Caller holds pairMu so a disk reader cannot publish an older pair afterward.
func (m *Manager) publishPair(id string, pair *tls.Certificate, certPEM, keyPEM []byte) {
	m.mu.Lock()
	m.cache[id] = &cachedCert{cert: pair, fingerprint: pairFingerprint(certPEM, keyPEM)}
	m.mu.Unlock()
}

// ReadPEM returns one part of the coherent on-disk pair for authenticated export.
func (m *Manager) ReadPEM(c *config.CertConf, key bool) ([]byte, error) {
	m.pairMu.RLock()
	defer m.pairMu.RUnlock()
	certFile, keyFile := m.certPath(c)
	if key {
		return os.ReadFile(keyFile)
	}
	return os.ReadFile(certFile)
}

func certificateKeyType(value string) (certcrypto.KeyType, error) {
	switch value {
	case "P256":
		return certcrypto.EC256, nil
	case "P384":
		return certcrypto.EC384, nil
	case "RSA2048":
		return certcrypto.RSA2048, nil
	case "RSA4096":
		return certcrypto.RSA4096, nil
	case "":
		return "", nil // Leave lego's existing default untouched.
	default:
		return "", errors.New("证书算法必须为 P256、P384、RSA2048 或 RSA4096")
	}
}

// RemoveFiles 删除证书对应的磁盘文件并清空缓存（删除配置时调用）。
func (m *Manager) RemoveFiles(c *config.CertConf) {
	m.pairMu.Lock()
	defer m.pairMu.Unlock()
	certFile, keyFile := m.certPath(c)
	os.Remove(certFile)
	os.Remove(keyFile)
	m.invalidate(c.ID)
}
