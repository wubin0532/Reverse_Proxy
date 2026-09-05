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
	loginMu            sync.Mutex
	failures           map[string][]time.Time
	twoFactorMu        sync.Mutex
	loginChallenges    map[string]*loginChallenge
	totpSetups         map[string]*totpSetup
	lastTOTPCounter    uint64
	hasLastTOTPCounter bool
}

func NewServer(cfg *config.Config, secure ...bool) *Server {
	isSecure := true
	if len(secure) > 0 {
		isSecure = secure[0]
	}
	return &Server{
		cfg: cfg, tokens: auth.NewTokenStore(), secure: isSecure,
		failures: make(map[string][]time.Time), loginChallenges: make(map[string]*loginChallenge),
		totpSetups: make(map[string]*totpSetup),
	}
}

// Mount 注册模块路由（会在已认证分组内调用 fn）。
// 由各模块包（import api）提供 RegisterRoutes，main 负责装配，避免 import cycle。
func (s *Server) Mount(fn func(chi.Router)) {
	s.mounters = append(s.mounters, fn)
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
	if hash == "" {
		valid = false
	} else {
		valid = subtle.ConstantTimeCompare([]byte(body.Username), []byte(user)) == 1 && auth.CheckPassword(hash, body.Password)
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
	if hash != "" && !auth.CheckPassword(hash, body.OldPassword) {
		Fail(w, 403, "原密码错误")
		return
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
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	cut := time.Now().Add(-5 * time.Minute)
	list := s.failures[ip]
	n := 0
	for _, t := range list {
		if t.After(cut) {
			list[n] = t
			n++
		}
	}
	list = list[:n]
	s.failures[ip] = list
	return len(list) >= 5
}
func (s *Server) recordLoginFailure(ip string) {
	s.loginMu.Lock()
	if len(s.failures) >= 1024 {
		cut := time.Now().Add(-5 * time.Minute)
		for key, attempts := range s.failures {
			if len(attempts) == 0 || attempts[len(attempts)-1].Before(cut) {
				delete(s.failures, key)
			}
		}
		// 大量伪造源地址也不能让限速状态无限占用内存。超过硬上限时
		// 淘汰任意旧桶；直接连接 IP 仍会在后续失败时重新建立计数。
		for len(s.failures) >= 4096 {
			for key := range s.failures {
				delete(s.failures, key)
				break
			}
		}
	}
	s.failures[ip] = append(s.failures[ip], time.Now())
	s.loginMu.Unlock()
}
func (s *Server) clearLoginFailures(ip string) {
	s.loginMu.Lock()
	delete(s.failures, ip)
	s.loginMu.Unlock()
}
