package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const tokenTTL = 24 * time.Hour

// maxTokenAge 会话 token 的绝对有效期上限：滑动续期不得使 token 存活超过签发后 7 天。
const maxTokenAge = 7 * 24 * time.Hour
const maxSessions = 256

// HashPassword 生成 bcrypt 哈希。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

func RandomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CheckPassword 校验明文与哈希。
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// tokenEntry 会话条目：滑动过期时间 + 签发时间（绝对有效期基准）。
type tokenEntry struct {
	expires   time.Time
	createdAt time.Time
}

// TokenStore 内存 token 会话存储。
type TokenStore struct {
	mu     sync.Mutex
	tokens map[[32]byte]tokenEntry // token 摘要 -> 会话条目
}

func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: make(map[[32]byte]tokenEntry)}
}

// Issue 签发新 token。
func (s *TokenStore) Issue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	digest := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	now := time.Now()
	for key, entry := range s.tokens {
		if !entry.expires.After(now) || !entry.createdAt.Add(maxTokenAge).After(now) {
			delete(s.tokens, key)
		}
	}
	if len(s.tokens) >= maxSessions {
		var oldestKey [32]byte
		var oldest time.Time
		for key, entry := range s.tokens {
			if oldest.IsZero() || entry.expires.Before(oldest) {
				oldestKey, oldest = key, entry.expires
			}
		}
		delete(s.tokens, oldestKey)
	}
	s.tokens[digest] = tokenEntry{expires: now.Add(tokenTTL), createdAt: now}
	s.mu.Unlock()
	return tok, nil
}

// Valid 校验 token 有效并滑动续期；续期后的过期时间不超过签发后 maxTokenAge 的绝对上限。
func (s *TokenStore) Valid(tok string) bool {
	digest := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[digest]
	now := time.Now()
	absolute := entry.createdAt.Add(maxTokenAge)
	if !ok || now.After(entry.expires) || !absolute.After(now) {
		delete(s.tokens, digest)
		return false
	}
	renewed := now.Add(tokenTTL)
	if renewed.After(absolute) {
		renewed = absolute
	}
	s.tokens[digest] = tokenEntry{expires: renewed, createdAt: entry.createdAt}
	return true
}

// Revoke 注销 token。
func (s *TokenStore) Revoke(tok string) {
	digest := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	delete(s.tokens, digest)
	s.mu.Unlock()
}

func (s *TokenStore) RevokeAll() {
	s.mu.Lock()
	s.tokens = make(map[[32]byte]tokenEntry)
	s.mu.Unlock()
}
