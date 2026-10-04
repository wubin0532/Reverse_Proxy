package api

import (
	"context"
	"net/http"
)

type admissionKey struct{}
type admissionCheck func() bool

// Modules can recheck admission after slow body/password verification.
func CurrentSession(r *http.Request) bool {
	check, ok := r.Context().Value(admissionKey{}).(admissionCheck)
	return !ok || check()
}

func (s *Server) admitSession(r *http.Request, token string) *http.Request {
	s.twoFactorMu.Lock()
	generation := s.authGeneration
	s.twoFactorMu.Unlock()
	check := admissionCheck(func() bool {
		s.twoFactorMu.Lock()
		defer s.twoFactorMu.Unlock()
		return generation == s.authGeneration && s.tokens.Valid(token)
	})
	return r.WithContext(context.WithValue(r.Context(), admissionKey{}, check))
}

func (s *Server) lockRequestCredentials(w http.ResponseWriter, r *http.Request, proof credentialProof) bool {
	if !s.lockCredentials(w, proof) {
		return false
	}
	cookie, err := r.Cookie(TokenCookie)
	if err != nil || !s.tokens.Valid(cookie.Value) {
		s.twoFactorMu.Unlock()
		Fail(w, 401, "未登录或登录已过期")
		return false
	}
	return true
}

// twoFactorMu protects credentials transitions, challenge consumption and
// session issuance. Expensive password verification happens outside this lock.
type credentialProof struct {
	generation         uint64
	user, hash, secret string
	totp               bool
}

func (s *Server) credentials() credentialProof {
	s.twoFactorMu.Lock()
	defer s.twoFactorMu.Unlock()
	return s.credentialsLocked()
}

func (s *Server) credentialsLocked() credentialProof {
	s.cfg.RLock()
	defer s.cfg.RUnlock()
	v := s.cfg.Settings
	return credentialProof{s.authGeneration, v.AdminUser, v.AdminPassHash, v.TOTPSecret, v.TOTPEnabled}
}

// On success the caller owns twoFactorMu until issuance/mutation is complete.
func (s *Server) lockCredentials(w http.ResponseWriter, proof credentialProof) bool {
	s.twoFactorMu.Lock()
	if proof != s.credentialsLocked() {
		s.twoFactorMu.Unlock()
		Fail(w, 409, "认证设置已变化，请重新登录后重试")
		return false
	}
	return true
}

func (s *Server) revokeSessionsLocked(w http.ResponseWriter) {
	s.authGeneration++
	s.tokens.RevokeAll()
	s.loginChallenges = make(map[string]*loginChallenge)
	s.totpSetups = make(map[string]*totpSetup)
	http.SetCookie(w, &http.Cookie{Name: TokenCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secure})
}
