package api

import (
	"fmt"
	"sync"
	"testing"
)

func TestFailureLimiterAdmitWindowAndClear(t *testing.T) {
	l := newFailureLimiter()
	for i := 0; i < 5; i++ {
		if !l.admit("password:192.0.2.1") {
			t.Fatalf("attempt %d should be admitted", i)
		}
	}
	if l.admit("password:192.0.2.1") {
		t.Fatal("6th attempt within window must be rejected")
	}
	// 不同操作与不同 IP 互不影响
	if !l.admit("logs:192.0.2.1") || !l.admit("password:192.0.2.2") {
		t.Fatal("limit should be scoped by operation+IP key")
	}
	l.clear("password:192.0.2.1")
	if !l.admit("password:192.0.2.1") {
		t.Fatal("clear should reset the attempt count")
	}
}

// S03 回归：尝试名额原子预占，并发请求不能一起穿过检查。
func TestFailureLimiterAdmitIsAtomicUnderConcurrency(t *testing.T) {
	l := newFailureLimiter()
	start := make(chan struct{})
	var wg sync.WaitGroup
	admitted := make(chan struct{}, 64)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if l.admit("192.0.2.1") {
				admitted <- struct{}{}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(admitted)
	if n := len(admitted); n != 5 {
		t.Fatalf("并发 12 次只应准入 5 次，实际 %d", n)
	}
}

// S05 回归：畸形请求同样在准入时占位，新桶统一经过容量控制。
func TestFailureLimiterBucketCapacityUnderFlood(t *testing.T) {
	l := newFailureLimiter()
	for i := 0; i < 5000; i++ {
		l.admit(fmt.Sprintf("198.51.%d.%d", i/256, i%256))
	}
	l.mu.Lock()
	n := len(l.attempts)
	l.mu.Unlock()
	if n > 4096 {
		t.Fatalf("attempt map grew to %d", n)
	}
}

func TestPasswordConfirmAdmitSharedByOperation(t *testing.T) {
	addr := "198.51.100.7:12345"
	defer ClearPasswordConfirmFailures("totp", addr)
	for i := 0; i < 5; i++ {
		if !AdmitPasswordConfirm("totp", addr) {
			t.Fatalf("attempt %d should be admitted", i)
		}
	}
	if AdmitPasswordConfirm("totp", addr) {
		t.Fatal("password confirm should be limited after 5 attempts")
	}
	ClearPasswordConfirmFailures("totp", addr)
	if !AdmitPasswordConfirm("totp", addr) {
		t.Fatal("clear should reset the password confirm limit")
	}
}
