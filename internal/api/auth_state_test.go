package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/backup"
	"github.com/pquerna/otp/totp"
)

func TestInFlightLoginCannotSurviveCredentialChanges(t *testing.T) {
	for _, kind := range []string{"password", "totp", "restore"} {
		t.Run(kind, func(t *testing.T) {
			s, h := testServer(t)
			admin := apiRequest(h, "POST", "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", "192.0.2.100:1", nil)
			cookie := admin.Result().Cookies()[0]
			started, resume := make(chan struct{}), make(chan struct{})
			s.checkLoginPassword = func(hash, password string) bool { close(started); <-resume; return auth.CheckPassword(hash, password) }
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				result <- apiRequest(h, "POST", "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", "192.0.2.101:1", nil)
			}()
			<-started
			var changed *httptest.ResponseRecorder
			switch kind {
			case "password":
				changed = apiRequest(h, "POST", "/api/settings/password", `{"oldPassword":"current-password","newPassword":"new-password-2026"}`, "https://router.local", "192.0.2.100:1", cookie)
			case "totp":
				setup := apiRequest(h, "POST", "/api/settings/totp/setup", `{"password":"current-password"}`, "https://router.local", "192.0.2.100:1", cookie)
				data := decodeTOTPEnvelope(t, setup.Body.String()).Data
				code, err := totp.GenerateCode(data.ManualKey, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(map[string]string{"setupId": data.SetupID, "code": code})
				changed = apiRequest(h, "POST", "/api/settings/totp/enable", string(body), "https://router.local", "192.0.2.100:1", cookie)
			case "restore":
				plain, err := s.cfg.PlainJSON()
				if err != nil {
					t.Fatal(err)
				}
				encrypted, err := backup.Encrypt(plain, "backup-password", "test", time.Now())
				if err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(map[string]string{"password": "current-password", "backupPassword": "backup-password", "backup": string(encrypted)})
				changed = apiRequest(h, "POST", "/api/system/backup/import", string(body), "https://router.local", "192.0.2.100:1", cookie)
			}
			close(resume)
			stale := <-result
			if changed.Code != 200 {
				t.Fatalf("credential change: %d %s", changed.Code, changed.Body.String())
			}
			if stale.Code != 409 || len(stale.Result().Cookies()) != 0 {
				t.Fatal("stale login issued a session/challenge")
			}
			if s.tokens.Valid(cookie.Value) {
				t.Fatal("old session still valid")
			}
		})
	}
}

func TestChallengeIsBoundToAuthenticationGeneration(t *testing.T) {
	s, h := testServer(t)
	s.twoFactorMu.Lock()
	id, err := s.issueLoginChallengeLocked("192.0.2.120")
	if err != nil {
		t.Fatal(err)
	}
	s.authGeneration++
	s.twoFactorMu.Unlock()
	body, _ := json.Marshal(map[string]string{"challengeId": id, "code": "000000"})
	res := apiRequest(h, "POST", "/api/login/totp", string(body), "https://router.local", "192.0.2.120:1", nil)
	if res.Code != 403 || len(res.Result().Cookies()) != 0 {
		t.Fatal("old generation challenge accepted")
	}
}

func TestRevokedSessionCannotCommitSlowPasswordRequest(t *testing.T) {
	s, h := testServer(t)
	login := apiRequest(h, "POST", "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", "192.0.2.110:1", nil)
	cookie := login.Result().Cookies()[0]
	started, resume := make(chan struct{}), make(chan struct{})
	body := &pausedCredentialBody{started: started, resume: resume, data: strings.NewReader(`{"oldPassword":"current-password","newPassword":"stale-password-2026"}`)}
	req := httptest.NewRequest("POST", "/api/settings/password", body)
	req.Host = "router.local"
	req.RemoteAddr = "192.0.2.110:1"
	req.Header.Set("Origin", "https://router.local")
	req.AddCookie(cookie)
	stale := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(stale, req); close(done) }()
	<-started
	changed := apiRequest(h, "POST", "/api/settings/password", `{"oldPassword":"current-password","newPassword":"fresh-password-2026"}`, "https://router.local", "192.0.2.111:1", cookie)
	if changed.Code != 200 {
		close(resume)
		<-done
		t.Fatal(changed.Body.String())
	}
	close(resume)
	<-done
	if stale.Code == 200 || auth.CheckPassword(s.credentials().hash, "stale-password-2026") {
		t.Fatal("revoked slow request changed credentials")
	}
}

type pausedCredentialBody struct {
	started, resume chan struct{}
	once            sync.Once
	data            *strings.Reader
}

func (b *pausedCredentialBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.resume })
	return b.data.Read(p)
}
func (b *pausedCredentialBody) Close() error { return nil }
