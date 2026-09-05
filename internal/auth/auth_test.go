package auth

import "testing"

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
