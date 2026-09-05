// Package notify 进程内事件总线与通用 Webhook 通知：
// 各模块通过包级 Publish 上报事件（证书/DDNS/监听异常等），
// 总线分发给订阅者（如 Webhook 推送），并保留最近事件供 Dashboard 展示。
package notify

import (
	"sync"
	"time"
)

// 事件级别。
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// 事件类型（按模块前缀归类，Webhook 订阅按前缀匹配）。
const (
	TypeCertObtainFailed  = "cert.obtain_failed"
	TypeCertObtainSuccess = "cert.obtain_success"
	TypeDDNSUpdateFailed  = "ddns.update_failed"
	TypeSiteListenError   = "site.listen_error"
	TypeFwdListenError    = "forward.listen_error"
	TypeTest              = "notify.test"
)

const (
	// recentCapacity Dashboard 展示的内存环形缓冲区容量。
	recentCapacity = 100
	// queueCapacity 待分发事件的缓冲队列容量，满则丢弃新事件并计数。
	queueCapacity = 256
)

// Event 一条通知事件。
type Event struct {
	Type    string    `json:"type"`           // 如 cert.obtain_failed / ddns.update_failed
	Entity  string    `json:"entity,omitempty"` // 关联实体名称（证书名、任务名、站点名等）
	Level   string    `json:"level"`          // info / warn / error
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// Bus 进程内事件总线：非阻塞发布，单 goroutine 顺序分发给订阅者。
type Bus struct {
	mu     sync.RWMutex
	subs   []func(Event)
	ring   []Event // 最近事件环形缓冲（按时间升序，超出容量丢弃最旧）
	queue  chan Event
	stop   chan struct{}
	once   sync.Once

	droppedMu sync.Mutex
	dropped   int64 // 队列满被丢弃的事件数
}

// NewBus 创建事件总线并启动分发 goroutine。
func NewBus() *Bus {
	b := &Bus{
		queue: make(chan Event, queueCapacity),
		stop:  make(chan struct{}),
	}
	go b.dispatch()
	return b
}

// Subscribe 注册订阅者，事件按发布顺序同步回调（回调应快速返回，耗时操作自行排队）。
func (b *Bus) Subscribe(fn func(Event)) {
	b.mu.Lock()
	b.subs = append(b.subs, fn)
	b.mu.Unlock()
}

// Publish 发布事件：写入环形缓冲并非阻塞入队分发；队列满时丢弃新事件并计数。
func (b *Bus) Publish(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.Level == "" {
		ev.Level = LevelInfo
	}
	b.mu.Lock()
	b.ring = append(b.ring, ev)
	if len(b.ring) > recentCapacity {
		b.ring = append([]Event(nil), b.ring[len(b.ring)-recentCapacity:]...)
	}
	b.mu.Unlock()
	select {
	case b.queue <- ev:
	default:
		b.droppedMu.Lock()
		b.dropped++
		b.droppedMu.Unlock()
	}
}

// Recent 返回最近 n 条事件（新的在前）。n<=0 取默认 20，上限 100。
func (b *Bus) Recent(n int) []Event {
	if n <= 0 {
		n = 20
	}
	if n > recentCapacity {
		n = recentCapacity
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Event, 0, n)
	for i := len(b.ring) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, b.ring[i])
	}
	return out
}

// Dropped 返回因队列满被丢弃的事件总数。
func (b *Bus) Dropped() int64 {
	b.droppedMu.Lock()
	defer b.droppedMu.Unlock()
	return b.dropped
}

// Close 停止分发 goroutine。
func (b *Bus) Close() { b.once.Do(func() { close(b.stop) }) }

// dispatch 顺序分发队列中的事件给全部订阅者；单个订阅者 panic 不影响其他订阅者。
func (b *Bus) dispatch() {
	for {
		select {
		case ev := <-b.queue:
			b.mu.RLock()
			subs := append([]func(Event){}, b.subs...)
			b.mu.RUnlock()
			for _, fn := range subs {
				func() {
					defer func() { _ = recover() }()
					fn(ev)
				}()
			}
		case <-b.stop:
			return
		}
	}
}

// 全局默认总线，参照 logcenter.SetDefault 模式：
// 各模块只调用包级 Publish，无需感知总线实例，也不会形成硬依赖。
var (
	defaultMu  sync.RWMutex
	defaultBus *Bus
)

// SetDefault 设置全局默认事件总线（main 装配时调用）。
func SetDefault(b *Bus) { defaultMu.Lock(); defaultBus = b; defaultMu.Unlock() }

// Default 返回全局默认事件总线，未设置时为 nil。
func Default() *Bus {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultBus
}

// Publish 向全局默认总线发布事件；未设置默认总线时静默忽略。
func Publish(ev Event) {
	if b := Default(); b != nil {
		b.Publish(ev)
	}
}

// Recent 返回全局默认总线的最近事件；未设置时返回空。
func Recent(n int) []Event {
	if b := Default(); b != nil {
		return b.Recent(n)
	}
	return []Event{}
}
