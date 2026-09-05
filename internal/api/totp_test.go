package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
)

type totpEnvelope struct {
	Data struct {
		SetupID         string   `json:"setupId"`
		ManualKey       string   `json:"manualKey"`
		ChallengeID     string   `json:"challengeId"`
		RecoveryCodes   []string `json:"recoveryCodes"`
		TwoFactorNeeded bool     `json:"twoFactorRequired"`
	} `json:"data"`
}

func decodeTOTPEnvelope(t *testing.T, body string) totpEnvelope {
	t.Helper()
	var result totpEnvelope
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("decode response %q: %v", body, err)
	}
	return result
}

func loginPassword(t *testing.T, handler http.Handler, ip string) totpEnvelope {
	t.Helper()
	rec := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", ip+":1000", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("password login = %d %s", rec.Code, rec.Body.String())
	}
	return decodeTOTPEnvelope(t, rec.Body.String())
}

func loginFactor(t *testing.T, handler http.Handler, ip, challengeID, code string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"challengeId": challengeID, "code": code})
	rec := apiRequest(handler, http.MethodPost, "/api/login/totp", string(body), "https://router.local", ip+":1000", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("factor login = %d %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" {
		t.Fatalf("factor login cookie = %+v", cookies)
	}
	return cookies[0]
}

func TestTOTPSetupLoginRecoveryManagementAndEncryption(t *testing.T) {
	s, handler := testServer(t)
	initial := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", "192.0.2.10:1000", nil)
	if initial.Code != http.StatusOK {
		t.Fatal(initial.Body.String())
	}
	cookie := initial.Result().Cookies()[0]

	setup := apiRequest(handler, http.MethodPost, "/api/settings/totp/setup", `{"password":"current-password"}`, "https://router.local", "192.0.2.10:1000", cookie)
	if setup.Code != http.StatusOK || setup.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	setupData := decodeTOTPEnvelope(t, setup.Body.String()).Data
	if len(setupData.SetupID) != 64 || setupData.ManualKey == "" {
		t.Fatalf("setup data = %+v", setupData)
	}
	qr := apiRequest(handler, http.MethodGet, "/api/settings/totp/setup/"+setupData.SetupID+"/qr", "", "", "192.0.2.10:1000", cookie)
	if qr.Code != http.StatusOK || qr.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(qr.Body.String(), "\x89PNG") || qr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("qr = %d %q", qr.Code, qr.Body.String())
	}
	code, err := totp.GenerateCode(setupData.ManualKey, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	enableBody, _ := json.Marshal(map[string]string{"setupId": setupData.SetupID, "code": code})
	enable := apiRequest(handler, http.MethodPost, "/api/settings/totp/enable", string(enableBody), "https://router.local", "192.0.2.10:1000", cookie)
	if enable.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", enable.Code, enable.Body.String())
	}
	recovery := decodeTOTPEnvelope(t, enable.Body.String()).Data.RecoveryCodes
	if len(recovery) != auth.RecoveryCodeCount {
		t.Fatalf("recovery count = %d", len(recovery))
	}
	if me := apiRequest(handler, http.MethodGet, "/api/me", "", "", "192.0.2.10:1000", cookie); me.Code != http.StatusUnauthorized {
		t.Fatalf("enable did not revoke session: %d", me.Code)
	}
	s.cfg.RLock()
	secret := s.cfg.Settings.TOTPSecret
	hashes := append([]string(nil), s.cfg.Settings.TOTPRecoveryHashes...)
	s.cfg.RUnlock()
	if secret != setupData.ManualKey || len(hashes) != auth.RecoveryCodeCount {
		t.Fatal("TOTP configuration was not persisted")
	}
	disk, err := os.ReadFile(filepath.Join(s.cfg.Dir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), secret) || strings.Contains(string(disk), recovery[0]) {
		t.Fatal("encrypted config leaked TOTP material")
	}

	challenge := loginPassword(t, handler, "192.0.2.20").Data.ChallengeID
	cross := apiRequest(handler, http.MethodPost, "/api/login/totp", `{"challengeId":"`+challenge+`","code":"`+code+`"}`, "https://router.local", "192.0.2.21:1000", nil)
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-IP challenge status = %d", cross.Code)
	}
	loginCookie := loginFactor(t, handler, "192.0.2.20", challenge, code)

	regenBody, _ := json.Marshal(map[string]string{"password": "current-password", "code": recovery[0]})
	regen := apiRequest(handler, http.MethodPost, "/api/settings/totp/recovery/regenerate", string(regenBody), "https://router.local", "192.0.2.20:1000", loginCookie)
	if regen.Code != http.StatusOK {
		t.Fatalf("regenerate = %d %s", regen.Code, regen.Body.String())
	}
	newRecovery := decodeTOTPEnvelope(t, regen.Body.String()).Data.RecoveryCodes
	if len(newRecovery) != auth.RecoveryCodeCount || newRecovery[0] == recovery[0] {
		t.Fatal("recovery codes were not replaced")
	}
	if me := apiRequest(handler, http.MethodGet, "/api/me", "", "", "192.0.2.20:1000", loginCookie); me.Code != http.StatusUnauthorized {
		t.Fatal("recovery regeneration did not revoke sessions")
	}

	oldChallenge := loginPassword(t, handler, "192.0.2.30").Data.ChallengeID
	oldBody, _ := json.Marshal(map[string]string{"challengeId": oldChallenge, "code": recovery[0]})
	old := apiRequest(handler, http.MethodPost, "/api/login/totp", string(oldBody), "https://router.local", "192.0.2.30:1000", nil)
	if old.Code != http.StatusForbidden {
		t.Fatalf("old recovery code status = %d", old.Code)
	}
	newChallenge := loginPassword(t, handler, "192.0.2.31").Data.ChallengeID
	newCookie := loginFactor(t, handler, "192.0.2.31", newChallenge, newRecovery[0])
	disableBody, _ := json.Marshal(map[string]string{"password": "current-password", "code": newRecovery[1]})
	disable := apiRequest(handler, http.MethodPost, "/api/settings/totp/disable", string(disableBody), "https://router.local", "192.0.2.31:1000", newCookie)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", disable.Code, disable.Body.String())
	}
	s.cfg.RLock()
	enabled, clearedSecret, remaining := s.cfg.Settings.TOTPEnabled, s.cfg.Settings.TOTPSecret, len(s.cfg.Settings.TOTPRecoveryHashes)
	s.cfg.RUnlock()
	if enabled || clearedSecret != "" || remaining != 0 {
		t.Fatal("disable did not clear TOTP material")
	}
}

func TestTOTPRejectsHTTPExpiredReplayAndExcessAttempts(t *testing.T) {
	s, _ := testServer(t)
	key, _ := auth.GenerateTOTP("admin")
	codes, hashes, _ := auth.GenerateRecoveryCodes()
	_ = codes
	if err := s.cfg.Update(func(c *config.Config) error {
		c.Settings.TOTPEnabled = true
		c.Settings.TOTPSecret = key.Secret()
		c.Settings.TOTPRecoveryHashes = hashes
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	insecure := NewServer(s.cfg, false).Router()
	login := apiRequest(insecure, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "http://router.local", "192.0.2.40:1000", nil)
	if login.Code != http.StatusForbidden {
		t.Fatalf("HTTP TOTP login status = %d", login.Code)
	}

	handler := s.Router()
	challenge := loginPassword(t, handler, "192.0.2.41").Data.ChallengeID
	s.twoFactorMu.Lock()
	s.loginChallenges[challenge].Expires = time.Now().Add(-time.Second)
	s.twoFactorMu.Unlock()
	expired := apiRequest(handler, http.MethodPost, "/api/login/totp", `{"challengeId":"`+challenge+`","code":"000000"}`, "https://router.local", "192.0.2.41:1000", nil)
	if expired.Code != http.StatusForbidden {
		t.Fatalf("expired challenge status = %d", expired.Code)
	}

	challenge = loginPassword(t, handler, "192.0.2.42").Data.ChallengeID
	for i := 0; i < 5; i++ {
		wrong := apiRequest(handler, http.MethodPost, "/api/login/totp", `{"challengeId":"`+challenge+`","code":"000000"}`, "https://router.local", "192.0.2.42:1000", nil)
		if wrong.Code != http.StatusForbidden {
			t.Fatalf("wrong attempt %d status = %d", i, wrong.Code)
		}
	}
	limited := apiRequest(handler, http.MethodPost, "/api/login/totp", `{"challengeId":"`+challenge+`","code":"000000"}`, "https://router.local", "192.0.2.42:1000", nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("TOTP attempt rate limit status = %d", limited.Code)
	}

	code, _ := totp.GenerateCode(key.Secret(), time.Now())
	firstChallenge := loginPassword(t, handler, "192.0.2.43").Data.ChallengeID
	_ = loginFactor(t, handler, "192.0.2.43", firstChallenge, code)
	secondChallenge := loginPassword(t, handler, "192.0.2.44").Data.ChallengeID
	replay := apiRequest(handler, http.MethodPost, "/api/login/totp", `{"challengeId":"`+secondChallenge+`","code":"`+code+`"}`, "https://router.local", "192.0.2.44:1000", nil)
	if replay.Code != http.StatusForbidden {
		t.Fatalf("replayed TOTP status = %d", replay.Code)
	}
}

func TestPasswordChangeRevokesPendingTOTPChallenge(t *testing.T) {
	s, _ := testServer(t)
	key, err := auth.GenerateTOTP("admin")
	if err != nil {
		t.Fatal(err)
	}
	recoveryCodes, hashes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cfg.Update(func(c *config.Config) error {
		c.Settings.TOTPEnabled = true
		c.Settings.TOTPSecret = key.Secret()
		c.Settings.TOTPRecoveryHashes = hashes
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := s.Router()

	challenge := loginPassword(t, handler, "192.0.2.50").Data.ChallengeID
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loginChallenge := loginPassword(t, handler, "192.0.2.51").Data.ChallengeID
	cookie := loginFactor(t, handler, "192.0.2.51", loginChallenge, code)

	change := apiRequest(handler, http.MethodPost, "/api/settings/password", `{"username":"admin","oldPassword":"current-password","newPassword":"changed-password-2026"}`, "https://router.local", "192.0.2.51:1000", cookie)
	if change.Code != http.StatusOK {
		t.Fatalf("change password = %d %s", change.Code, change.Body.String())
	}

	stale := apiRequest(handler, http.MethodPost, "/api/login/totp", `{"challengeId":"`+challenge+`","code":"`+recoveryCodes[0]+`"}`, "https://router.local", "192.0.2.50:1000", nil)
	if stale.Code != http.StatusForbidden {
		t.Fatalf("pending challenge survived password change: %d %s", stale.Code, stale.Body.String())
	}
}
