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

// TokenStore 内存 token 会话存储。
type TokenStore struct {
	mu     sync.Mutex
	tokens map[[32]byte]time.Time // token 摘要 -> 过期时间
}

func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: make(map[[32]byte]time.Time)}
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
	for key, expires := range s.tokens {
		if !expires.After(now) {
			delete(s.tokens, key)
		}
	}
	if len(s.tokens) >= maxSessions {
		var oldestKey [32]byte
		var oldest time.Time
		for key, expires := range s.tokens {
			if oldest.IsZero() || expires.Before(oldest) {
				oldestKey, oldest = key, expires
			}
		}
		delete(s.tokens, oldestKey)
	}
	s.tokens[digest] = now.Add(tokenTTL)
	s.mu.Unlock()
	return tok, nil
}

// Valid 校验 token 有效并滑动续期。
func (s *TokenStore) Valid(tok string) bool {
	digest := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[digest]
	if !ok || time.Now().After(exp) {
		delete(s.tokens, digest)
		return false
	}
	s.tokens[digest] = time.Now().Add(tokenTTL)
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
	s.tokens = make(map[[32]byte]time.Time)
	s.mu.Unlock()
}
