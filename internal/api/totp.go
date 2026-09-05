package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"image/png"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pquerna/otp"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
)

const (
	loginChallengeTTL  = 5 * time.Minute
	setupTTL           = 10 * time.Minute
	maxLoginChallenges = 256
	maxTOTPSetups      = 8
)

type loginChallenge struct {
	IP       string
	Expires  time.Time
	Attempts int
}

type totpSetup struct {
	Secret         string
	URL            string
	SessionBinding [32]byte
	Expires        time.Time
}

func secureRandomID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Server) issueLoginChallenge(ip string) (string, error) {
	id, err := secureRandomID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	s.twoFactorMu.Lock()
	defer s.twoFactorMu.Unlock()
	for key, challenge := range s.loginChallenges {
		if !challenge.Expires.After(now) {
			delete(s.loginChallenges, key)
		}
	}
	for len(s.loginChallenges) >= maxLoginChallenges {
		for key := range s.loginChallenges {
			delete(s.loginChallenges, key)
			break
		}
	}
	s.loginChallenges[id] = &loginChallenge{IP: ip, Expires: now.Add(loginChallengeTTL)}
	return id, nil
}

func (s *Server) handleTOTPLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.secure {
		Fail(w, 403, "双重验证只允许通过 HTTPS 使用")
		return
	}
	ip := directIP(r.RemoteAddr)
	if s.loginLimited(ip) {
		Fail(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
		return
	}
	var body struct {
		ChallengeID string `json:"challengeId"`
		Code        string `json:"code"`
	}
	if DecodeBody(r, &body) != nil || len(body.ChallengeID) != 64 || len(body.Code) > 32 {
		s.recordLoginFailure(ip)
		Fail(w, 403, "双重验证码无效或已过期")
		return
	}
	s.twoFactorMu.Lock()
	challenge := s.loginChallenges[body.ChallengeID]
	if challenge == nil || !challenge.Expires.After(time.Now()) || challenge.Attempts >= 5 {
		delete(s.loginChallenges, body.ChallengeID)
		s.twoFactorMu.Unlock()
		s.recordLoginFailure(ip)
		Fail(w, 403, "双重验证码无效或已过期")
		return
	}
	if challenge.IP != ip {
		s.twoFactorMu.Unlock()
		s.recordLoginFailure(ip)
		Fail(w, 403, "双重验证码无效或已过期")
		return
	}
	challenge.Attempts++
	ok := s.verifyFactorLocked(strings.TrimSpace(body.Code), true)
	if ok {
		delete(s.loginChallenges, body.ChallengeID)
	}
	s.twoFactorMu.Unlock()
	if !ok {
		s.recordLoginFailure(ip)
		log.Printf("[security] 双重验证登录失败，客户端 IP: %s", ip)
		Fail(w, 403, "双重验证码无效或已过期")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.finishLogin(w, ip)
}

func (s *Server) verifyFactorLocked(code string, consumeRecovery bool) bool {
	s.cfg.RLock()
	enabled := s.cfg.Settings.TOTPEnabled
	secret := s.cfg.Settings.TOTPSecret
	hashes := append([]string(nil), s.cfg.Settings.TOTPRecoveryHashes...)
	s.cfg.RUnlock()
	if !enabled || secret == "" {
		return false
	}
	if counter, valid := auth.ValidateTOTP(secret, code, time.Now()); valid {
		if s.hasLastTOTPCounter && counter <= s.lastTOTPCounter {
			return false
		}
		s.lastTOTPCounter = counter
		s.hasLastTOTPCounter = true
		// 计数器随配置落盘，防止进程重启后同一动态码在 30~90 秒窗口内被重用。
		// 登录成功是低频事件，每次验证成功写一次可接受。
		if err := s.cfg.Update(func(c *config.Config) error {
			c.Settings.TOTPLastCounter = int64(counter)
			return nil
		}); err != nil {
			log.Printf("[security] 保存 TOTP 计数器失败: %v", err)
		}
		return true
	}
	index := auth.FindRecoveryCode(hashes, code)
	if index < 0 {
		return false
	}
	if !consumeRecovery {
		return true
	}
	return s.cfg.Update(func(c *config.Config) error {
		current := auth.FindRecoveryCode(c.Settings.TOTPRecoveryHashes, code)
		if current < 0 {
			return errInvalidFactor
		}
		c.Settings.TOTPRecoveryHashes = append(c.Settings.TOTPRecoveryHashes[:current], c.Settings.TOTPRecoveryHashes[current+1:]...)
		return nil
	}) == nil
}

var errInvalidFactor = &factorError{}

type factorError struct{}

func (*factorError) Error() string { return "invalid factor" }

func (s *Server) requireSecureTOTP(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !s.secure {
		Fail(w, 403, "Google Authenticator 只能通过 HTTPS 设置和管理")
		return false
	}
	return true
}

func sessionBinding(r *http.Request) ([32]byte, bool) {
	cookie, err := r.Cookie(TokenCookie)
	if err != nil || cookie.Value == "" {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(cookie.Value)), true
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecureTOTP(w) {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if DecodeBody(r, &body) != nil {
		Fail(w, 403, "当前密码错误")
		return
	}
	if PasswordConfirmLimited("totp", r.RemoteAddr) {
		Fail(w, http.StatusTooManyRequests, "密码错误次数过多，请稍后再试")
		return
	}
	if !s.checkCurrentPassword(body.Password) {
		RecordPasswordConfirmFailure("totp", r.RemoteAddr)
		Fail(w, 403, "当前密码错误")
		return
	}
	ClearPasswordConfirmFailures("totp", r.RemoteAddr)
	s.cfg.RLock()
	enabled, account := s.cfg.Settings.TOTPEnabled, s.cfg.Settings.AdminUser
	s.cfg.RUnlock()
	if enabled {
		Fail(w, 409, "Google Authenticator 已启用")
		return
	}
	binding, ok := sessionBinding(r)
	if !ok {
		Fail(w, 401, "未登录或登录已过期")
		return
	}
	key, err := auth.GenerateTOTP(account)
	if err != nil {
		Fail(w, 500, "生成双重验证密钥失败")
		return
	}
	id, err := secureRandomID()
	if err != nil {
		Fail(w, 500, "生成绑定任务失败")
		return
	}
	s.twoFactorMu.Lock()
	now := time.Now()
	for setupID, setup := range s.totpSetups {
		if !setup.Expires.After(now) {
			delete(s.totpSetups, setupID)
		}
	}
	for len(s.totpSetups) >= maxTOTPSetups {
		for setupID := range s.totpSetups {
			delete(s.totpSetups, setupID)
			break
		}
	}
	s.totpSetups[id] = &totpSetup{Secret: key.Secret(), URL: key.URL(), SessionBinding: binding, Expires: now.Add(setupTTL)}
	s.twoFactorMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	OK(w, map[string]interface{}{"setupId": id, "manualKey": key.Secret(), "expiresIn": 600})
}

func (s *Server) lookupSetup(r *http.Request, id string) (*totpSetup, bool) {
	binding, ok := sessionBinding(r)
	if !ok {
		return nil, false
	}
	setup := s.totpSetups[id]
	if setup == nil || !setup.Expires.After(time.Now()) || subtle.ConstantTimeCompare(setup.SessionBinding[:], binding[:]) != 1 {
		delete(s.totpSetups, id)
		return nil, false
	}
	return setup, true
}

func (s *Server) handleTOTPSetupQR(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecureTOTP(w) {
		return
	}
	s.twoFactorMu.Lock()
	setup, ok := s.lookupSetup(r, chi.URLParam(r, "setupId"))
	if !ok {
		s.twoFactorMu.Unlock()
		Fail(w, 404, "绑定任务不存在或已过期")
		return
	}
	setupURL := setup.URL
	s.twoFactorMu.Unlock()
	key, err := otp.NewKeyFromURL(setupURL)
	if err != nil {
		Fail(w, 500, "二维码生成失败")
		return
	}
	image, err := key.Image(256, 256)
	if err != nil {
		Fail(w, 500, "二维码生成失败")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_ = png.Encode(w, image)
}

func (s *Server) handleTOTPSetupCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecureTOTP(w) {
		return
	}
	s.twoFactorMu.Lock()
	id := chi.URLParam(r, "setupId")
	if _, ok := s.lookupSetup(r, id); ok {
		delete(s.totpSetups, id)
	}
	s.twoFactorMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	OK(w, nil)
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	if !s.requireSecureTOTP(w) {
		return
	}
	var body struct {
		SetupID string `json:"setupId"`
		Code    string `json:"code"`
	}
	if DecodeBody(r, &body) != nil {
		Fail(w, 400, "请求格式错误")
		return
	}
	s.twoFactorMu.Lock()
	setup, ok := s.lookupSetup(r, body.SetupID)
	if !ok {
		s.twoFactorMu.Unlock()
		Fail(w, 404, "绑定任务不存在或已过期")
		return
	}
	if _, valid := auth.ValidateTOTP(setup.Secret, strings.TrimSpace(body.Code), time.Now()); !valid {
		s.twoFactorMu.Unlock()
		Fail(w, 403, "动态验证码错误")
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes()
	if err == nil {
		err = s.cfg.Update(func(c *config.Config) error {
			if c.Settings.TOTPEnabled {
				return errInvalidFactor
			}
			c.Settings.TOTPEnabled = true
			c.Settings.TOTPSecret = setup.Secret
			c.Settings.TOTPRecoveryHashes = hashes
			return nil
		})
	}
	delete(s.totpSetups, body.SetupID)
	s.hasLastTOTPCounter = false
	s.twoFactorMu.Unlock()
	if err != nil {
		Fail(w, 500, "启用双重验证失败")
		return
	}
	s.revokeAllSessions(w)
	w.Header().Set("Cache-Control", "no-store")
	log.Printf("[security] Google Authenticator 已启用，全部会话已撤销")
	OK(w, map[string]interface{}{"recoveryCodes": codes, "loginRequired": true})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	s.handleTOTPManagement(w, r, false)
}

func (s *Server) handleTOTPRecoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	s.handleTOTPManagement(w, r, true)
}

func (s *Server) handleTOTPManagement(w http.ResponseWriter, r *http.Request, regenerate bool) {
	if !s.requireSecureTOTP(w) {
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if DecodeBody(r, &body) != nil {
		Fail(w, 403, "当前密码或双重验证码错误")
		return
	}
	if PasswordConfirmLimited("totp", r.RemoteAddr) {
		Fail(w, http.StatusTooManyRequests, "密码错误次数过多，请稍后再试")
		return
	}
	if !s.checkCurrentPassword(body.Password) {
		RecordPasswordConfirmFailure("totp", r.RemoteAddr)
		Fail(w, 403, "当前密码或双重验证码错误")
		return
	}
	ClearPasswordConfirmFailures("totp", r.RemoteAddr)
	s.twoFactorMu.Lock()
	if !s.verifyFactorLocked(strings.TrimSpace(body.Code), false) {
		s.twoFactorMu.Unlock()
		Fail(w, 403, "当前密码或双重验证码错误")
		return
	}
	var codes, hashes []string
	var err error
	if regenerate {
		codes, hashes, err = auth.GenerateRecoveryCodes()
	}
	if err == nil {
		err = s.cfg.Update(func(c *config.Config) error {
			if !c.Settings.TOTPEnabled {
				return errInvalidFactor
			}
			if regenerate {
				c.Settings.TOTPRecoveryHashes = hashes
			} else {
				c.Settings.TOTPEnabled = false
				c.Settings.TOTPSecret = ""
				c.Settings.TOTPRecoveryHashes = nil
			}
			return nil
		})
	}
	if !regenerate {
		s.hasLastTOTPCounter = false
	}
	s.twoFactorMu.Unlock()
	if err != nil {
		Fail(w, 500, "保存双重验证设置失败")
		return
	}
	s.revokeAllSessions(w)
	w.Header().Set("Cache-Control", "no-store")
	if regenerate {
		log.Printf("[security] 双重验证恢复码已重新生成，全部会话已撤销")
		OK(w, map[string]interface{}{"recoveryCodes": codes, "loginRequired": true})
	} else {
		log.Printf("[security] Google Authenticator 已关闭，全部会话已撤销")
		OK(w, map[string]bool{"loginRequired": true})
	}
}

func (s *Server) checkCurrentPassword(password string) bool {
	s.cfg.RLock()
	hash := s.cfg.Settings.AdminPassHash
	s.cfg.RUnlock()
	return hash != "" && len(password) <= 72 && auth.CheckPassword(hash, password)
}

func (s *Server) revokeAllSessions(w http.ResponseWriter) {
	s.tokens.RevokeAll()
	s.twoFactorMu.Lock()
	s.loginChallenges = make(map[string]*loginChallenge)
	s.totpSetups = make(map[string]*totpSetup)
	s.twoFactorMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: TokenCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secure})
}
