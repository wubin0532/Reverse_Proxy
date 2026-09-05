package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
)

func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("current-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Lock()
	cfg.Settings.AdminUser = "admin"
	cfg.Settings.AdminPassHash = hash
	cfg.Settings.MustChangePassword = false
	cfg.Unlock()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, true)
	return s, s.Router()
}

func apiRequest(handler http.Handler, method, path, body, origin, remote string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "router.local"
	req.RemoteAddr = remote
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestOriginRateLimitSecureCookieAndSessionRevocation(t *testing.T) {
	_, handler := testServer(t)
	malicious := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "https://evil.example", "192.0.2.1:1000", nil)
	if malicious.Code != http.StatusForbidden {
		t.Fatalf("malicious origin status=%d", malicious.Code)
	}
	wrongScheme := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "http://router.local", "192.0.2.1:1000", nil)
	if wrongScheme.Code != http.StatusForbidden {
		t.Fatalf("wrong origin scheme status=%d", wrongScheme.Code)
	}
	for i := 0; i < 5; i++ {
		rec := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"wrong"}`, "https://router.local", "192.0.2.2:1000", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("failure %d status=%d", i, rec.Code)
		}
	}
	limited := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"wrong"}`, "https://router.local", "192.0.2.2:1000", nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit status=%d", limited.Code)
	}
	login := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", "192.0.2.3:1000", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("insecure cookie: %+v", cookies)
	}
	changed := apiRequest(handler, http.MethodPost, "/api/settings/password", `{"username":"admin","oldPassword":"current-password","newPassword":"new-password-2026"}`, "https://router.local", "192.0.2.3:1000", cookies[0])
	if changed.Code != http.StatusOK {
		t.Fatalf("change password status=%d body=%s", changed.Code, changed.Body.String())
	}
	me := apiRequest(handler, http.MethodGet, "/api/me", "", "", "192.0.2.3:1000", cookies[0])
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("old session remained valid: %d", me.Code)
	}
}

func TestLoginFailureBucketsAreBounded(t *testing.T) {
	s, _ := testServer(t)
	for i := 0; i < 5000; i++ {
		s.recordLoginFailure(fmt.Sprintf("192.0.2.%d", i))
	}
	s.loginLimiter.mu.Lock()
	count := len(s.loginLimiter.failures)
	s.loginLimiter.mu.Unlock()
	if count > 4096 {
		t.Fatalf("login failure map grew to %d", count)
	}
}
