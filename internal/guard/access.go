package guard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"golang.org/x/crypto/bcrypt"
)

// maxAccessAttempts Basic 认证尝试表的硬上限。
const maxAccessAttempts = 4096

const maxAccessSessions = 2048

var accessDummyHash = func() string { hash, _ := auth.HashPassword("dummy-business-password"); return hash }()
var accessVerifySlots = make(chan struct{}, 2)

type AccessSession struct {
	ID             string    `json:"id"`
	GroupID        string    `json:"groupId"`
	UserID         string    `json:"userId"`
	Username       string    `json:"username"`
	IP             string    `json:"ip"`
	Host           string    `json:"host"`
	EntityID       string    `json:"entityId"`
	Source         string    `json:"source"`
	Mode           string    `json:"mode"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	fingerprint    string
	tokenHash      [32]byte
	credentialHash [32]byte
}
type accessAttempt struct {
	at    time.Time
	count int
}
type accessRuntime struct {
	mu       sync.Mutex
	sessions map[string]AccessSession
	attempts map[string]accessAttempt
}

func (e *Engine) accessSnapshot(id string) (config.AccessGroup, []config.AccessUser, string, bool) {
	if e == nil {
		return config.AccessGroup{}, nil, "", false
	}
	e.cfg.RLock()
	defer e.cfg.RUnlock()
	var group config.AccessGroup
	found := false
	for _, g := range e.cfg.AccessGroups {
		if g.ID == id {
			group = g
			group.UserIDs = append([]string(nil), g.UserIDs...)
			found = true
			break
		}
	}
	if !found || !group.Enabled {
		return group, nil, "", false
	}
	users := []config.AccessUser{}
	for _, id := range group.UserIDs {
		for _, u := range e.cfg.AccessUsers {
			if u.ID == id && u.Enabled {
				users = append(users, u)
				break
			}
		}
	}
	b, _ := json.Marshal(struct {
		Group config.AccessGroup
		Users []config.AccessUser
	}{group, users})
	sum := sha256.Sum256(b)
	return group, users, hex.EncodeToString(sum[:]), true
}
func (a *accessRuntime) initLocked() {
	if a.sessions == nil {
		a.sessions = map[string]AccessSession{}
	}
	if a.attempts == nil {
		a.attempts = map[string]accessAttempt{}
	}
}
func (a *accessRuntime) pruneLocked(now time.Time) {
	a.initLocked()
	for id, s := range a.sessions {
		if !s.ExpiresAt.After(now) {
			delete(a.sessions, id)
		}
	}
	for key, v := range a.attempts {
		if now.Sub(v.at) > time.Minute {
			delete(a.attempts, key)
		}
	}
}
func (e *Engine) matchingSession(group config.AccessGroup, fingerprint, ip, host, entity, source string, token, credentials [32]byte) (AccessSession, bool) {
	e.access.mu.Lock()
	defer e.access.mu.Unlock()
	e.access.pruneLocked(time.Now())
	for id, s := range e.access.sessions {
		if s.GroupID != group.ID {
			continue
		}
		if s.fingerprint != fingerprint {
			delete(e.access.sessions, id)
			continue
		}
		if s.IP != ip {
			continue
		}
		scope := s.Source == source && s.EntityID == entity && s.Host == host
		explicitIP := s.Mode == "ip" && scope
		crossIP := group.CrossWebIPBypass && source == "web" && s.Source == "web" && s.Mode == "cookie"
		cookie := scope && token != ([32]byte{}) && s.tokenHash == token
		basicCache := scope && credentials != ([32]byte{}) && s.credentialHash == credentials
		if explicitIP || crossIP || cookie || basicCache {
			return s, true
		}
	}
	return AccessSession{}, false
}
func (e *Engine) storeAccessSession(s AccessSession) {
	e.access.mu.Lock()
	defer e.access.mu.Unlock()
	e.access.pruneLocked(time.Now())
	if len(e.access.sessions) >= maxAccessSessions {
		var oldest AccessSession
		for _, existing := range e.access.sessions {
			if oldest.ID == "" || existing.CreatedAt.Before(oldest.CreatedAt) {
				oldest = existing
			}
		}
		delete(e.access.sessions, oldest.ID)
	}
	e.access.sessions[s.ID] = s
}
func (e *Engine) attemptAccess(group, ip string) bool {
	e.access.mu.Lock()
	defer e.access.mu.Unlock()
	e.access.pruneLocked(time.Now())
	now := time.Now()
	key := group + "|" + ip
	v, ok := e.access.attempts[key]
	if !ok {
		if len(e.access.attempts) >= maxAccessAttempts {
			// 表满时淘汰最旧的桶，而不是拒绝：否则分布式扫描会把所有新来源
			// （包括凭据正确的正常用户）一起 429，构成对安全组认证的拒绝服务。
			e.access.evictOldestAttemptLocked()
		}
		v.at = now
	}
	v.count++
	e.access.attempts[key] = v
	return v.count <= 10
}

// evictOldestAttemptLocked 采样淘汰最旧的尝试桶，避免每次新建 key 都全表扫描。
func (a *accessRuntime) evictOldestAttemptLocked() {
	const sample = 64
	var oldestKey string
	var oldest time.Time
	seen := 0
	for key, v := range a.attempts {
		if oldestKey == "" || v.at.Before(oldest) {
			oldestKey, oldest = key, v.at
		}
		seen++
		if seen >= sample {
			break
		}
	}
	if oldestKey != "" {
		delete(a.attempts, oldestKey)
	}
}
func AccessCookieName(id string) string { return "andey-access-" + id }

// Authenticate business requests independently of the administrator account.
// Sessions bind to IP, Host and rule; cross-rule IP reuse is explicit opt-in.
func (e *Engine) AuthorizeHTTP(w http.ResponseWriter, r *http.Request, id, ip, entity string, secure bool) bool {
	if id == "" {
		return true
	}
	group, users, fingerprint, ok := e.accessSnapshot(id)
	if !ok {
		http.Error(w, "403 Access group unavailable", 403)
		return false
	}
	ip = canonicalAccessIP(ip)
	if ip == "" || len(r.Host) > 253 {
		http.Error(w, "400 Invalid client identity", 400)
		return false
	}
	host := strings.ToLower(r.Host)
	if !secure {
		parsed := net.ParseIP(ip)
		if parsed == nil || !parsed.IsLoopback() {
			http.Error(w, "426 HTTPS required for group authentication", 426)
			return false
		}
	}
	username, password, hasBasic := r.BasicAuth()
	var tokenHash, credentialHash [32]byte
	if c, err := r.Cookie(AccessCookieName(id)); err == nil && len(c.Value) <= 128 {
		tokenHash = sha256.Sum256([]byte(c.Value))
	}
	if hasBasic && len(username) <= 64 && len(password) <= 72 {
		credentialHash = sha256.Sum256([]byte(username + "\x00" + password))
	}
	if _, ok := e.matchingSession(group, fingerprint, ip, host, entity, "web", tokenHash, credentialHash); ok {
		return true
	}
	if !hasBasic || len(username) > 64 || len(password) > 72 {
		return e.challengeAccess(w, ip, entity)
	}
	if !e.attemptAccess(id, ip) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "429 Authentication attempts exceeded", 429)
		return false
	}
	var user config.AccessUser
	for _, u := range users {
		if u.Username == username {
			user = u
			break
		}
	}

	select {
	case accessVerifySlots <- struct{}{}:
	default:
		http.Error(w, "429 Authentication busy", 429)
		return false
	}
	hash := user.PasswordHash
	if user.ID == "" {
		hash = accessDummyHash
	}
	valid := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil && user.ID != ""
	<-accessVerifySlots
	if !valid {
		return e.challengeAccess(w, ip, entity)
	}
	// Recheck after bcrypt, so disabling a user while verification runs cannot
	// create a newly valid session from the obsolete credential snapshot.
	_, _, current, active := e.accessSnapshot(id)
	if !active || current != fingerprint {
		return e.challengeAccess(w, ip, entity)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "500 Cannot create session", 500)
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s := AccessSession{ID: ids.New(), GroupID: id, UserID: user.ID, Username: user.Username, IP: ip, Host: host, EntityID: entity, Source: "web", Mode: "cookie", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Duration(group.SessionMinutes) * time.Minute), fingerprint: fingerprint, tokenHash: sha256.Sum256([]byte(token)), credentialHash: credentialHash}
	e.storeAccessSession(s)
	http.SetCookie(w, &http.Cookie{Name: AccessCookieName(id), Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, Expires: s.ExpiresAt, MaxAge: group.SessionMinutes * 60})
	return true
}
func canonicalAccessIP(ip string) string {
	if p := net.ParseIP(ip); p != nil {
		return p.String()
	}
	return ""
}
func (e *Engine) challengeAccess(w http.ResponseWriter, ip, entity string) bool {
	e.RecordRejection(ip, "web", entity, "安全组认证失败")
	w.Header().Set("WWW-Authenticate", `Basic realm="andey-access", charset="UTF-8"`)
	http.Error(w, "401 Authentication required", 401)
	return false
}
func (e *Engine) CheckForwardAccess(id, ip, entity string) bool {
	if id == "" {
		return true
	}
	g, _, fingerprint, ok := e.accessSnapshot(id)
	if !ok {
		return false
	}
	_, ok = e.matchingSession(g, fingerprint, canonicalAccessIP(ip), "", entity, "forward", [32]byte{}, [32]byte{})
	return ok
}
func (e *Engine) AccessSessions() []AccessSession {
	e.access.mu.Lock()
	e.access.pruneLocked(time.Now())
	out := make([]AccessSession, 0, len(e.access.sessions))
	for _, s := range e.access.sessions {
		out = append(out, s)
	}
	e.access.mu.Unlock()
	valid := out[:0]
	for _, s := range out {
		_, _, fingerprint, ok := e.accessSnapshot(s.GroupID)
		if ok && fingerprint == s.fingerprint {
			valid = append(valid, s)
		} else {
			e.RevokeAccessSession(s.ID)
		}
	}
	return valid
}
func (e *Engine) RevokeAccessSession(id string) {
	e.access.mu.Lock()
	delete(e.access.sessions, id)
	e.access.mu.Unlock()
}
func (e *Engine) AddAccessGrant(s AccessSession) error {
	g, users, fingerprint, ok := e.accessSnapshot(s.GroupID)
	if !ok {
		return fmt.Errorf("安全组不可用")
	}
	found := false
	for _, u := range users {
		if u.ID == s.UserID {
			found = true
			s.Username = u.Username
			break
		}
	}
	if !found {
		return fmt.Errorf("授权用户不属于本组或已停用")
	}
	if net.ParseIP(s.IP) == nil || !s.ExpiresAt.After(time.Now()) || s.ExpiresAt.After(time.Now().Add(time.Duration(g.SessionMinutes)*time.Minute)) {
		return fmt.Errorf("授权IP或到期时间无效")
	}
	e.cfg.RLock()
	bound := false
	if s.Source == "web" {
		for _, site := range e.cfg.Sites {
			for _, rule := range site.Rules {
				if rule.ID == s.EntityID && rule.AccessGroupID == s.GroupID && s.Host != "" && len(s.Host) <= 253 && !strings.ContainsAny(s.Host, " /\\\r\n") {
					bound = true
				}
			}
		}
	}
	if s.Source == "forward" {
		for _, rule := range e.cfg.Forwards {
			if rule.ID == s.EntityID && rule.AccessGroupID == s.GroupID && s.Host == "" {
				bound = true
			}
		}
	}
	e.cfg.RUnlock()
	if !bound {
		return fmt.Errorf("授权作用域未绑定安全组")
	}
	s.ID = ids.New()
	s.IP = canonicalAccessIP(s.IP)
	s.Host = strings.ToLower(s.Host)
	s.CreatedAt = time.Now().UTC()
	s.Mode = "ip"
	s.fingerprint = fingerprint
	e.storeAccessSession(s)
	return nil
}
