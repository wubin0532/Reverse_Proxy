package api

import (
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
)

const TokenCookie = "andey-proxy_token"

// Server 管理后台 API 服务。
type Server struct {
	cfg                *config.Config
	tokens             *auth.TokenStore
	mounters           []func(chi.Router)
	secure             bool
	loginLimiter       *failureLimiter
	twoFactorMu        sync.Mutex
	backupMu           sync.Mutex // one backup import/export at a time (scrypt memory)
	loginChallenges    map[string]*loginChallenge
	totpSetups         map[string]*totpSetup
	lastTOTPCounter    uint64
	hasLastTOTPCounter bool
	version            string // 备份文件元信息里的应用版本
	restoreHook        func() // 配置导入后的热重载回调（main 装配各服务 Reload）
}

func NewServer(cfg *config.Config, secure ...bool) *Server {
	isSecure := true
	if len(secure) > 0 {
		isSecure = secure[0]
	}
	s := &Server{
		cfg: cfg, tokens: auth.NewTokenStore(), secure: isSecure,
		loginLimiter: newFailureLimiter(), loginChallenges: make(map[string]*loginChallenge),
		totpSetups: make(map[string]*totpSetup),
	}
	// 恢复上次验证成功的 TOTP 计数器，防止进程重启后同一动态码在窗口内被重用。
	cfg.RLock()
	if cfg.Settings.TOTPLastCounter > 0 {
		s.lastTOTPCounter = uint64(cfg.Settings.TOTPLastCounter)
		s.hasLastTOTPCounter = true
	}
	cfg.RUnlock()
	return s
}

// Mount 注册模块路由（会在已认证分组内调用 fn）。
// 由各模块包（import api）提供 RegisterRoutes，main 负责装配，避免 import cycle。
func (s *Server) Mount(fn func(chi.Router)) {
	s.mounters = append(s.mounters, fn)
}

// SetConfigRestore 设置配置备份所需的版本号与导入成功后的热重载回调。
// hook 由各服务的 Reload 组成，在导入落盘成功后调用。
func (s *Server) SetConfigRestore(version string, hook func()) {
	s.version = version
	s.restoreHook = hook
}

// Router 构建 API 路由。
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.securityHeaders)
	r.Use(s.verifyOrigin)

	r.Post("/api/login", s.handleLogin)
	r.Post("/api/login/totp", s.handleTOTPLogin)
	r.Post("/api/logout", s.handleLogout)

	r.Group(func(r chi.Router) {
		r.Use(s.requireAuth)
		r.Get("/api/me", s.handleMe)
		r.Post("/api/settings/password", s.handleChangePassword)
		r.Post("/api/settings/totp/setup", s.handleTOTPSetup)
		r.Get("/api/settings/totp/setup/{setupId}/qr", s.handleTOTPSetupQR)
		r.Delete("/api/settings/totp/setup/{setupId}", s.handleTOTPSetupCancel)
		r.Post("/api/settings/totp/enable", s.handleTOTPEnable)
		r.Post("/api/settings/totp/disable", s.handleTOTPDisable)
		r.Post("/api/settings/totp/recovery/regenerate", s.handleTOTPRecoveryRegenerate)
		r.Post("/api/system/backup/export", s.handleBackupExport)
		r.Post("/api/system/backup/import", s.handleBackupImport)
		for _, m := range s.mounters {
			m(r)
		}
	})

	return r
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) verifyOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				expectedScheme := "http"
				if s.secure {
					expectedScheme = "https"
				}
				if err != nil || !strings.EqualFold(u.Scheme, expectedScheme) || !strings.EqualFold(u.Host, r.Host) || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
					Fail(w, 403, "请求来源不可信")
					return
				}
			} else if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				Fail(w, 403, "请求来源不可信")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(TokenCookie)
		if err != nil || !s.tokens.Valid(c.Value) {
			Fail(w, 401, "未登录或登录已过期")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// dummyLoginHash 用户名不匹配时用于执行等时 bcrypt 校验的占位哈希，
// 避免通过响应耗时探测管理用户名是否存在。
var dummyLoginHash = func() string {
	h, err := auth.HashPassword("dummy-password-for-timing")
	if err != nil {
		panic(err) // bcrypt 默认参数下不会失败
	}
	return h
}()

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := directIP(r.RemoteAddr)
	if s.loginLimited(ip) {
		Fail(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := DecodeBody(r, &body); err != nil {
		Fail(w, 400, "请求格式错误")
		return
	}
	if len(body.Username) > 64 || len(body.Password) > 72 {
		s.recordLoginFailure(ip)
		Fail(w, 403, "账号或密码错误")
		return
	}
	s.cfg.RLock()
	user := s.cfg.Settings.AdminUser
	hash := s.cfg.Settings.AdminPassHash
	totpEnabled := s.cfg.Settings.TOTPEnabled
	s.cfg.RUnlock()

	valid := false
	if hash != "" && subtle.ConstantTimeCompare([]byte(body.Username), []byte(user)) == 1 {
		valid = auth.CheckPassword(hash, body.Password)
	} else {
		// 用户名不匹配（或尚未初始化密码）时也执行一次 bcrypt 校验，
		// 保持两条路径耗时一致，结果丢弃。
		_ = auth.CheckPassword(dummyLoginHash, body.Password)
	}
	if !valid {
		s.recordLoginFailure(ip)
		log.Printf("[security] 登录失败，客户端 IP: %s", ip)
		Fail(w, 403, "账号或密码错误")
		return
	}
	if totpEnabled {
		if !s.secure {
			Fail(w, 403, "已启用双重验证，必须通过 HTTPS 登录")
			return
		}
		challengeID, err := s.issueLoginChallenge(ip)
		if err != nil {
			Fail(w, 500, "无法创建双重验证挑战")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		OK(w, map[string]interface{}{"twoFactorRequired": true, "challengeId": challengeID, "expiresIn": 300})
		return
	}
	s.finishLogin(w, ip)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(TokenCookie); err == nil {
		s.tokens.Revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: TokenCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secure})
	OK(w, nil)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	defer s.cfg.RUnlock()
	OK(w, map[string]interface{}{
		"username":           s.cfg.Settings.AdminUser,
		"needChangePassword": s.cfg.Settings.MustChangePassword,
		"totpEnabled":        s.cfg.Settings.TOTPEnabled,
	})
}

func (s *Server) finishLogin(w http.ResponseWriter, ip string) {
	s.clearLoginFailures(ip)
	tok, err := s.tokens.Issue()
	if err != nil {
		log.Printf("[security] 生成会话令牌失败: %v", err)
		Fail(w, 500, "无法创建安全会话")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: TokenCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secure, MaxAge: int((24 * time.Hour).Seconds()), Expires: time.Now().Add(24 * time.Hour)})
	s.cfg.RLock()
	mustChange := s.cfg.Settings.MustChangePassword
	totpEnabled := s.cfg.Settings.TOTPEnabled
	s.cfg.RUnlock()
	log.Printf("[security] 管理员登录成功，客户端 IP: %s", ip)
	OK(w, map[string]interface{}{"needChangePassword": mustChange, "twoFactorRequired": false, "totpEnabled": totpEnabled})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := DecodeBody(r, &body); err != nil {
		Fail(w, 400, "请求格式错误")
		return
	}
	s.cfg.RLock()
	hash := s.cfg.Settings.AdminPassHash
	user := s.cfg.Settings.AdminUser
	s.cfg.RUnlock()
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		body.Username = user
	}
	if len(body.Username) > 64 || strings.ContainsAny(body.Username, "\r\n\t") {
		Fail(w, 400, "管理账号必须为 1 到 64 字节且不能包含控制字符")
		return
	}
	if utf8.RuneCountInString(body.NewPassword) < 10 || len(body.NewPassword) > 72 {
		Fail(w, 400, "新密码至少 10 个字符且不超过 72 字节")
		return
	}
	if hash != "" {
		if PasswordConfirmLimited("password", r.RemoteAddr) {
			Fail(w, http.StatusTooManyRequests, "密码错误次数过多，请稍后再试")
			return
		}
		if !auth.CheckPassword(hash, body.OldPassword) {
			RecordPasswordConfirmFailure("password", r.RemoteAddr)
			Fail(w, 403, "原密码错误")
			return
		}
		ClearPasswordConfirmFailures("password", r.RemoteAddr)
	}
	newHash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		Fail(w, 500, "密码加密失败")
		return
	}
	if err := s.cfg.Update(func(c *config.Config) error {
		c.Settings.AdminUser = body.Username
		c.Settings.AdminPassHash = newHash
		c.Settings.MustChangePassword = false
		return nil
	}); err != nil {
		Fail(w, 500, "保存配置失败")
		return
	}
	log.Printf("[security] 管理账号密码已修改，全部会话已撤销")
	s.revokeAllSessions(w)
	OK(w, map[string]bool{"loginRequired": true})
}

func directIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return remote
}

func (s *Server) loginLimited(ip string) bool {
	return s.loginLimiter.limited(ip)
}
func (s *Server) recordLoginFailure(ip string) {
	s.loginLimiter.record(ip)
}
func (s *Server) clearLoginFailures(ip string) {
	s.loginLimiter.clear(ip)
}
