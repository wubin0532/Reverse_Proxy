package guard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func accessFixture(t *testing.T) (*config.Config, *Engine) {
	t.Helper()
	c, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.State().Close() })
	hash, _ := auth.HashPassword("fixture-password")
	if err := c.Update(func(c *config.Config) error {
		c.AccessUsers = []config.AccessUser{{ID: "user", Revision: 1, Username: "test", Enabled: true, PasswordHash: hash}}
		c.AccessGroups = []config.AccessGroup{{ID: "group", Revision: 1, Name: "Group", Enabled: true, SessionMinutes: 30, UserIDs: []string{"user"}}}
		c.Sites = []config.Site{{ID: "site", Rules: []config.SubRule{{ID: "r1", Type: "reverse", AccessGroupID: "group"}, {ID: "r2", Type: "reverse", AccessGroupID: "group"}}}}
		c.Forwards = []config.ForwardRule{{ID: "tcp", AccessGroupID: "group"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return c, NewEngine(c)
}
func accessRequest(e *Engine, host, entity, ip string, cookie *http.Cookie, basic bool) (bool, *httptest.ResponseRecorder) {
	r := httptest.NewRequest("GET", "https://"+host+"/", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if basic {
		r.SetBasicAuth("test", "fixture-password")
	}
	w := httptest.NewRecorder()
	ok := e.AuthorizeHTTP(w, r, "group", ip, entity, true)
	return ok, w
}
func TestAccessSessionScopeExpiryAndRevocation(t *testing.T) {
	c, e := accessFixture(t)
	ok, w := accessRequest(e, "app.example", "r1", "203.0.113.7", nil, true)
	if !ok || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal(cookie)
	}
	if ok, _ := accessRequest(e, "app.example", "r1", "203.0.113.7", cookie, false); !ok {
		t.Fatal("valid session rejected")
	}
	for _, v := range []struct{ host, rule, ip string }{{"other.example", "r1", "203.0.113.7"}, {"app.example", "r2", "203.0.113.7"}, {"app.example", "r1", "203.0.113.8"}} {
		if ok, _ := accessRequest(e, v.host, v.rule, v.ip, cookie, false); ok {
			t.Fatal("scope bypass", v)
		}
	}
	sessions := e.AccessSessions()
	if len(sessions) != 1 {
		t.Fatal(sessions)
	}
	e.RevokeAccessSession(sessions[0].ID)
	if ok, _ := accessRequest(e, "app.example", "r1", "203.0.113.7", cookie, false); ok {
		t.Fatal("revoked cookie accepted")
	}
	_, w = accessRequest(e, "app.example", "r1", "203.0.113.7", nil, true)
	cookie = w.Result().Cookies()[0]
	c.Update(func(c *config.Config) error { c.AccessUsers[0].Enabled = false; return nil })
	if ok, _ := accessRequest(e, "app.example", "r1", "203.0.113.7", cookie, false); ok {
		t.Fatal("disabled user's cookie survived")
	}
	c.Update(func(c *config.Config) error {
		c.AccessUsers[0].Enabled = true
		c.AccessGroups[0].CrossWebIPBypass = true
		return nil
	})
	accessRequest(e, "app.example", "r1", "203.0.113.7", nil, true)
	if ok, _ := accessRequest(e, "other.example", "r2", "203.0.113.7", nil, false); !ok {
		t.Fatal("explicit cross-rule IP opt-in not applied")
	}
	if e.CheckForwardAccess("group", "203.0.113.7", "tcp") {
		t.Fatal("Web sign-in implicitly authorized forward")
	}
	if err := e.AddAccessGrant(AccessSession{GroupID: "group", UserID: "user", IP: "203.0.113.7", Source: "forward", EntityID: "tcp", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if !e.CheckForwardAccess("group", "203.0.113.7", "tcp") || e.CheckForwardAccess("group", "203.0.113.7", "other") {
		t.Fatal("forward grant scope")
	}
	for _, s := range e.AccessSessions() {
		if s.Source == "forward" {
			e.access.mu.Lock()
			s.ExpiresAt = time.Now().Add(-time.Second)
			e.access.sessions[s.ID] = s
			e.access.mu.Unlock()
		}
	}
	if e.CheckForwardAccess("group", "203.0.113.7", "tcp") {
		t.Fatal("expired grant accepted")
	}
	if err := e.AddAccessGrant(AccessSession{GroupID: "group", UserID: "user", IP: "203.0.113.7", Source: "forward", EntityID: "unknown", ExpiresAt: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("grant for unbound entity accepted")
	}
}
func TestAccessAPIWriteOnlyPasswordAndRevision(t *testing.T) {
	c, e := accessFixture(t)
	r := chi.NewRouter()
	RegisterRoutes(r, c, e)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/protection/access/users", nil))
	if bytes.Contains(w.Body.Bytes(), []byte("$2a$")) || bytes.Contains(w.Body.Bytes(), []byte("passwordHash")) {
		t.Fatal("password hash exposed")
	}
	body := map[string]any{"revision": 1, "name": "Updated", "username": "test", "enabled": true, "password": ""}
	b, _ := json.Marshal(body)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/api/protection/access/users/user", bytes.NewReader(b)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	c.RLock()
	hash := c.AccessUsers[0].PasswordHash
	c.RUnlock()
	if !auth.CheckPassword(hash, "fixture-password") {
		t.Fatal("blank edit cleared password")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/api/protection/access/users/user", bytes.NewReader(b)))
	if w.Code != 409 {
		t.Fatal("stale user revision accepted", w.Code)
	}
	req := httptest.NewRequest("DELETE", "/api/protection/access/groups/group", nil)
	req.Header.Set("If-Match", "1")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatal("referenced group deleted", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/protection/access/users", bytes.NewBufferString(`{"username":"new","enabled":true,"password":"short"}`)))
	if w.Code != 400 {
		t.Fatal("weak new password accepted")
	}
}

func TestBusinessAccessVerificationHasSeparateBudget(t *testing.T) {
	_, e := accessFixture(t)
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < 4; i++ {
		release, ok := auth.AcquireVerifySlot(time.Millisecond)
		if !ok {
			t.Fatal("cannot occupy admin verification budget")
		}
		releases = append(releases, release)
	}
	if ok, w := accessRequest(e, "app.example", "r1", "203.0.113.9", nil, true); !ok {
		t.Fatal("business auth depended on occupied admin budget", w.Code)
	}
}
