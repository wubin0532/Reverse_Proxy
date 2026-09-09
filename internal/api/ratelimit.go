package api

import (
	"sync"
	"time"
)

// failureLimiter 按 key 预占尝试名额的滑动窗口限速器（5 次尝试/5 分钟窗口）。
// 登录按直连 IP 做 key，已认证接口的密码二次确认按 "操作类型+直连IP" 做 key。
type failureLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newFailureLimiter() *failureLimiter {
	return &failureLimiter{attempts: make(map[string][]time.Time)}
}

// admit 原子地检查并预占一次尝试名额：达到上限返回 false，否则立即计入
// 一次尝试（无论随后请求是否合法、验证是否通过）。名额必须在读取请求体
// 与密码校验之前预占，并发请求才能受配置限额约束；验证成功后调用 clear 清零。
func (l *failureLimiter) admit(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-5 * time.Minute)
	list := l.attempts[key]
	n := 0
	for _, t := range list {
		if t.After(cut) {
			list[n] = t
			n++
		}
	}
	list = list[:n]
	if len(list) >= 5 {
		l.attempts[key] = list
		return false
	}
	if _, ok := l.attempts[key]; !ok {
		l.evictLocked(cut)
	}
	l.attempts[key] = append(list, time.Now())
	return true
}

// evictLocked 在新建桶之前执行容量控制：先淘汰窗口外的旧桶，仍超过硬上限时
// 淘汰任意旧桶。大量伪造源地址也不能让限速状态无限占用内存；直接连接 IP
// 仍会在后续尝试时重新建立计数。
func (l *failureLimiter) evictLocked(cut time.Time) {
	if len(l.attempts) < 1024 {
		return
	}
	for k, attempts := range l.attempts {
		if len(attempts) == 0 || attempts[len(attempts)-1].Before(cut) {
			delete(l.attempts, k)
		}
	}
	for len(l.attempts) >= 4096 {
		for k := range l.attempts {
			delete(l.attempts, k)
			break
		}
	}
}

// clear 清零 key 的尝试计数（验证成功后调用）。
func (l *failureLimiter) clear(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

// confirmLimiter 已认证接口密码二次确认（修改密码、安装更新、清空日志、
// 双重验证管理）共享的限速器，key 为 "操作类型:直连IP"。
var confirmLimiter = newFailureLimiter()

func confirmKey(op, remoteAddr string) string {
	return op + ":" + directIP(remoteAddr)
}

// AdmitPasswordConfirm 为指定操作的密码确认原子预占一次尝试名额；
// 返回 false 表示已触发限速。验证成功后必须调用 ClearPasswordConfirmFailures。
func AdmitPasswordConfirm(op, remoteAddr string) bool {
	return confirmLimiter.admit(confirmKey(op, remoteAddr))
}

// ClearPasswordConfirmFailures 在密码确认成功后清零计数。
func ClearPasswordConfirmFailures(op, remoteAddr string) {
	confirmLimiter.clear(confirmKey(op, remoteAddr))
}
