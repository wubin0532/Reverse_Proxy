package auth

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestTokenStoreUsesDigestsAndBoundsSessions(t *testing.T) {
	s := NewTokenStore()
	first, err := s.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Valid(first) {
		t.Fatal("new token should be valid")
	}
	for i := 0; i < maxSessions+20; i++ {
		if _, err := s.Issue(); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	count := len(s.tokens)
	s.mu.Unlock()
	if count > maxSessions {
		t.Fatalf("session store grew to %d", count)
	}
	if s.Valid(first) {
		t.Fatal("oldest token should have been evicted")
	}
}

func TestTokenStoreAbsoluteExpiry(t *testing.T) {
	s := NewTokenStore()
	tok, err := s.Issue()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(tok))
	// 把签发时间拨回绝对有效期上限之前，滑动续期也不能让其继续存活。
	s.mu.Lock()
	entry := s.tokens[digest]
	entry.createdAt = time.Now().Add(-maxTokenAge - time.Hour)
	s.tokens[digest] = entry
	s.mu.Unlock()
	if s.Valid(tok) {
		t.Fatal("token past its absolute age should be invalid")
	}
}

func TestTokenStoreSlidingRenewalCappedByAbsoluteAge(t *testing.T) {
	s := NewTokenStore()
	tok, err := s.Issue()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(tok))
	// 签发时间临近绝对上限时，续期后的过期时间不得超过 createdAt + maxTokenAge。
	s.mu.Lock()
	entry := s.tokens[digest]
	entry.createdAt = time.Now().Add(-maxTokenAge + time.Hour)
	s.tokens[digest] = entry
	s.mu.Unlock()
	if !s.Valid(tok) {
		t.Fatal("token within its absolute age should stay valid")
	}
	s.mu.Lock()
	renewed := s.tokens[digest]
	s.mu.Unlock()
	if renewed.expires.After(renewed.createdAt.Add(maxTokenAge)) {
		t.Fatalf("renewal exceeded absolute age: expires=%s createdAt=%s", renewed.expires, renewed.createdAt)
	}
}
