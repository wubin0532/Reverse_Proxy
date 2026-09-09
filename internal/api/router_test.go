package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
		s.admitLogin(fmt.Sprintf("192.0.2.%d", i))
	}
	s.loginLimiter.mu.Lock()
	count := len(s.loginLimiter.attempts)
	s.loginLimiter.mu.Unlock()
	if count > 4096 {
		t.Fatalf("login attempt map grew to %d", count)
	}
}

// S03 回归：同 IP 并发登录受配置限额约束，超出并发的请求得到 429。
func TestConcurrentLoginsRespectLimit(t *testing.T) {
	_, handler := testServer(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"wrong"}`, "https://router.local", "192.0.2.9:1000", nil)
			codes <- rec.Code
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	denied, limited := 0, 0
	for code := range codes {
		switch code {
		case http.StatusForbidden:
			denied++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	if denied > 5 {
		t.Fatalf("超过限额的请求进入了密码校验: %d 次 403", denied)
	}
	if limited == 0 {
		t.Fatal("并发超出限额时应出现 429")
	}
}

// S06 回归：首次强制改密期间，服务端只允许个人状态、改密与退出接口。
func TestMustChangePasswordIsEnforcedServerSide(t *testing.T) {
	s, handler := testServer(t)
	if err := s.cfg.Update(func(c *config.Config) error {
		c.Settings.MustChangePassword = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	login := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", "192.0.2.50:1000", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if me := apiRequest(handler, http.MethodGet, "/api/me", "", "", "192.0.2.50:1000", cookie); me.Code != http.StatusOK {
		t.Fatalf("/api/me status=%d", me.Code)
	}
	blocked := apiRequest(handler, http.MethodPost, "/api/system/backup/export", `{"password":"current-password","backupPassword":"backup-pass-1"}`, "https://router.local", "192.0.2.50:1000", cookie)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("写路由在强制改密期间应返回 403，实际 %d", blocked.Code)
	}
	changed := apiRequest(handler, http.MethodPost, "/api/settings/password", `{"username":"admin","oldPassword":"current-password","newPassword":"new-password-2026"}`, "https://router.local", "192.0.2.50:1000", cookie)
	if changed.Code != http.StatusOK {
		t.Fatalf("改密 status=%d body=%s", changed.Code, changed.Body.String())
	}
	login2 := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"new-password-2026"}`, "https://router.local", "192.0.2.51:1000", nil)
	if login2.Code != http.StatusOK {
		t.Fatalf("relogin status=%d", login2.Code)
	}
	cookie2 := login2.Result().Cookies()[0]
	export := apiRequest(handler, http.MethodPost, "/api/system/backup/export", `{"password":"new-password-2026","backupPassword":"backup-pass-1"}`, "https://router.local", "192.0.2.51:1000", cookie2)
	if export.Code != http.StatusOK {
		t.Fatalf("改密后写路由应可用，实际 %d body=%s", export.Code, export.Body.String())
	}
}

// S04 回归：声明了请求体却一直不发送的慢请求会被读取期限终止。
func TestSlowBodyReadIsTerminated(t *testing.T) {
	s, handler := testServer(t)
	s.bodyReadTimeout = 50 * time.Millisecond
	srv := httptest.NewServer(handler)
	defer srv.Close()
	pr, pw := io.Pipe()
	defer pw.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/login", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	done := make(chan struct{})
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("慢请求体未被读取期限终止")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("读取期限生效过晚: %v", elapsed)
	}
}
