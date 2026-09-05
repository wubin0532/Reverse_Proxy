package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/backup"
	"andey-proxy/internal/config"
)

// 登录拿会话 cookie。
func loginCookie(t *testing.T, handler http.Handler, remote string) *http.Cookie {
	t.Helper()
	rec := apiRequest(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"current-password"}`, "https://router.local", remote, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func TestBackupExportImportRoundTrip(t *testing.T) {
	s, handler := testServer(t)
	hookCalled := false
	s.SetConfigRestore("test-version", func() { hookCalled = true })
	remote := "192.0.2.10:1000"
	cookie := loginCookie(t, handler, remote)

	// 导出
	exp := apiRequest(handler, http.MethodPost, "/api/system/backup/export", `{"password":"current-password","backupPassword":"backup-pass-123"}`, "https://router.local", remote, cookie)
	if exp.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", exp.Code, exp.Body.String())
	}
	if cd := exp.Header().Get("Content-Disposition"); !strings.Contains(cd, "andey-proxy-backup-") {
		t.Fatalf("missing attachment disposition: %q", cd)
	}
	if cc := exp.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("missing no-store: %q", cc)
	}
	// 用同口令解密导出文件，应还原出配置明文 JSON
	plain, err := backup.Decrypt(exp.Body.Bytes(), "backup-pass-123")
	if err != nil {
		t.Fatalf("decrypt exported backup: %v", err)
	}
	var restored config.Config
	if err := json.Unmarshal(plain, &restored); err != nil {
		t.Fatalf("decrypted payload is not config JSON: %v", err)
	}
	if restored.Settings.AdminUser != "admin" || restored.Settings.AdminPassHash == "" {
		t.Fatalf("unexpected decrypted config: %+v", restored.Settings)
	}

	// 错误备份口令解密失败
	if _, err := backup.Decrypt(exp.Body.Bytes(), "wrong-backup-pass"); err == nil {
		t.Fatal("wrong backup password should fail decryption")
	}

	// 导入（把用户名改成 imported-admin 以验证覆盖生效）
	backupJSON, _ := json.Marshal(map[string]interface{}{
		"settings":  map[string]interface{}{"adminUser": "imported-admin", "adminPassHash": restored.Settings.AdminPassHash, "adminPort": 16601},
		"providers": []interface{}{}, "ddns": []interface{}{}, "certs": []interface{}{},
		"sites": []interface{}{}, "forwards": []interface{}{},
	})
	backupFile, err := backup.Encrypt(backupJSON, "backup-pass-123", "test-version", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	imp := apiRequest(handler, http.MethodPost, "/api/system/backup/import", mustJSON(t, map[string]string{
		"password": "current-password", "backupPassword": "backup-pass-123", "backup": string(backupFile),
	}), "https://router.local", remote, cookie)
	if imp.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", imp.Code, imp.Body.String())
	}
	s.cfg.RLock()
	user := s.cfg.Settings.AdminUser
	s.cfg.RUnlock()
	if user != "imported-admin" {
		t.Fatalf("config not replaced, adminUser=%q", user)
	}
	if !hookCalled {
		t.Fatal("restore hook not called")
	}
	// 导入前当前配置应已备份为 config.json.bak
	if _, err := os.Stat(filepath.Join(s.cfg.Dir(), "config.json.bak")); err != nil {
		t.Fatalf("config.json.bak not created: %v", err)
	}
	// 导入后旧会话必须已撤销
	me := apiRequest(handler, http.MethodGet, "/api/me", "", "", remote, cookie)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("old session should be revoked, got %d", me.Code)
	}
}

func TestBackupPasswordConfirmRateLimit(t *testing.T) {
	_, handler := testServer(t)
	remote := "192.0.2.11:1000"
	cookie := loginCookie(t, handler, remote)
	for i := 0; i < 5; i++ {
		rec := apiRequest(handler, http.MethodPost, "/api/system/backup/export", `{"password":"wrong","backupPassword":"backup-pass-123"}`, "https://router.local", remote, cookie)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("failure %d status=%d", i, rec.Code)
		}
	}
	limited := apiRequest(handler, http.MethodPost, "/api/system/backup/export", `{"password":"current-password","backupPassword":"backup-pass-123"}`, "https://router.local", remote, cookie)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit status=%d", limited.Code)
	}
	// 同一限速桶也保护导入接口
	limitedImp := apiRequest(handler, http.MethodPost, "/api/system/backup/import", `{"password":"current-password","backupPassword":"backup-pass-123","backup":"{}"}`, "https://router.local", remote, cookie)
	if limitedImp.Code != http.StatusTooManyRequests {
		t.Fatalf("import rate limit status=%d", limitedImp.Code)
	}
}

func TestBackupValidation(t *testing.T) {
	_, handler := testServer(t)
	remote := "192.0.2.12:1000"
	cookie := loginCookie(t, handler, remote)
	// 备份口令太短
	short := apiRequest(handler, http.MethodPost, "/api/system/backup/export", `{"password":"current-password","backupPassword":"short"}`, "https://router.local", remote, cookie)
	if short.Code != http.StatusBadRequest {
		t.Fatalf("short backup password status=%d", short.Code)
	}
	// 未登录
	noAuth := apiRequest(handler, http.MethodPost, "/api/system/backup/export", `{"password":"current-password","backupPassword":"backup-pass-123"}`, "https://router.local", "192.0.2.13:1000", nil)
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", noAuth.Code)
	}
	// 导入内容不是备份文件
	bad := apiRequest(handler, http.MethodPost, "/api/system/backup/import", `{"password":"current-password","backupPassword":"backup-pass-123","backup":"{\"foo\":1}"}`, "https://router.local", remote, cookie)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid backup status=%d", bad.Code)
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
