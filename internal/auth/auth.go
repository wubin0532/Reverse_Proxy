package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const tokenTTL = 24 * time.Hour

// HashPassword 生成 bcrypt 哈希。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验明文与哈希。
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// TokenStore 内存 token 会话存储。
type TokenStore struct {
	mu      sync.Mutex
	tokens  map[string]time.Time // token -> 过期时间
}

func NewTokenStore() *TokenStore {
	return &TokenStore{tokens: make(map[string]time.Time)}
}

// Issue 签发新 token。
func (s *TokenStore) Issue() string {
	buf := make([]byte, 32)
	rand.Read(buf)
	tok := hex.EncodeToString(buf)
	s.mu.Lock()
	s.tokens[tok] = time.Now().Add(tokenTTL)
	s.mu.Unlock()
	return tok
}

// Valid 校验 token 有效并滑动续期。
func (s *TokenStore) Valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[tok]
	if !ok || time.Now().After(exp) {
		delete(s.tokens, tok)
		return false
	}
	s.tokens[tok] = time.Now().Add(tokenTTL)
	return true
}

// Revoke 注销 token。
func (s *TokenStore) Revoke(tok string) {
	s.mu.Lock()
	delete(s.tokens, tok)
	s.mu.Unlock()
}
