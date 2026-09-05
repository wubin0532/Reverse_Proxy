package api

import "testing"

func TestFailureLimiterWindowAndClear(t *testing.T) {
	l := newFailureLimiter()
	for i := 0; i < 5; i++ {
		if l.limited("password:192.0.2.1") {
			t.Fatalf("failure %d should not be limited", i)
		}
		l.record("password:192.0.2.1")
	}
	if !l.limited("password:192.0.2.1") {
		t.Fatal("5 failures within window should be limited")
	}
	// 不同操作与不同 IP 互不影响
	if l.limited("logs:192.0.2.1") || l.limited("password:192.0.2.2") {
		t.Fatal("limit should be scoped by operation+IP key")
	}
	l.clear("password:192.0.2.1")
	if l.limited("password:192.0.2.1") {
		t.Fatal("clear should reset the failure count")
	}
}

func TestPasswordConfirmLimiterSharedByOperation(t *testing.T) {
	addr := "198.51.100.7:12345"
	defer ClearPasswordConfirmFailures("totp", addr)
	for i := 0; i < 5; i++ {
		RecordPasswordConfirmFailure("totp", addr)
	}
	if !PasswordConfirmLimited("totp", addr) {
		t.Fatal("password confirm should be limited after 5 failures")
	}
	ClearPasswordConfirmFailures("totp", addr)
	if PasswordConfirmLimited("totp", addr) {
		t.Fatal("clear should reset the password confirm limit")
	}
}
