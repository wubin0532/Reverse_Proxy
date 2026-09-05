package api

import (
	"sync"
	"time"
)

// failureLimiter 按 key 记录失败次数的滑动窗口限速器（5 次失败/5 分钟窗口）。
// 登录按直连 IP 做 key，已认证接口的密码二次确认按 "操作类型+直连 IP" 做 key。
type failureLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

func newFailureLimiter() *failureLimiter {
	return &failureLimiter{failures: make(map[string][]time.Time)}
}

// limited 报告 key 是否已达到失败上限，同时清理窗口外的旧记录。
func (l *failureLimiter) limited(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-5 * time.Minute)
	list := l.failures[key]
	n := 0
	for _, t := range list {
		if t.After(cut) {
			list[n] = t
			n++
		}
	}
	list = list[:n]
	l.failures[key] = list
	return len(list) >= 5
}

// record 记录一次失败；map 超过硬上限时淘汰旧桶，避免伪造源地址耗尽内存。
func (l *failureLimiter) record(key string) {
	l.mu.Lock()
	if len(l.failures) >= 1024 {
		cut := time.Now().Add(-5 * time.Minute)
		for k, attempts := range l.failures {
			if len(attempts) == 0 || attempts[len(attempts)-1].Before(cut) {
				delete(l.failures, k)
			}
		}
		// 大量伪造源地址也不能让限速状态无限占用内存。超过硬上限时
		// 淘汰任意旧桶；直接连接 IP 仍会在后续失败时重新建立计数。
		for len(l.failures) >= 4096 {
			for k := range l.failures {
				delete(l.failures, k)
				break
			}
		}
	}
	l.failures[key] = append(l.failures[key], time.Now())
	l.mu.Unlock()
}

// clear 清零 key 的失败计数（验证成功后调用）。
func (l *failureLimiter) clear(key string) {
	l.mu.Lock()
	delete(l.failures, key)
	l.mu.Unlock()
}

// confirmLimiter 已认证接口密码二次确认（修改密码、安装更新、清空日志、
// 双重验证管理）共享的限速器，key 为 "操作类型:直连IP"。
var confirmLimiter = newFailureLimiter()

func confirmKey(op, remoteAddr string) string {
	return op + ":" + directIP(remoteAddr)
}

// PasswordConfirmLimited 报告指定操作的密码确认是否已触发限速。
func PasswordConfirmLimited(op, remoteAddr string) bool {
	return confirmLimiter.limited(confirmKey(op, remoteAddr))
}

// RecordPasswordConfirmFailure 在密码确认失败时计数（仅在密码错误时调用）。
func RecordPasswordConfirmFailure(op, remoteAddr string) {
	confirmLimiter.record(confirmKey(op, remoteAddr))
}

// ClearPasswordConfirmFailures 在密码确认成功后清零计数。
func ClearPasswordConfirmFailures(op, remoteAddr string) {
	confirmLimiter.clear(confirmKey(op, remoteAddr))
}
