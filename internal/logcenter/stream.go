package logcenter

import (
	"sync"
	"time"
)

const (
	maxStreamClients = 8                // 并发日志流客户端上限
	streamQueueSize  = 64               // 每客户端缓冲条数
	streamHeartbeat  = 25 * time.Second // 心跳间隔（SSE 注释行）
)

type subscriber struct {
	q  Query
	ch chan Entry
}

type streamHub struct {
	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	closed bool
}

func newStreamHub() *streamHub {
	return &streamHub{subs: make(map[*subscriber]struct{})}
}

// subscribe 超过并发上限或 Center 已关闭时返回 false。
func (h *streamHub) subscribe(q Query) (*subscriber, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.subs) >= maxStreamClients {
		return nil, false
	}
	s := &subscriber{q: q, ch: make(chan Entry, streamQueueSize)}
	h.subs[s] = struct{}{}
	return s, true
}

func (h *streamHub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// publish 慢消费者策略：缓冲已满时丢弃最旧一条再写入，保证最新事件优先送达。
func (h *streamHub) publish(e Entry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !entryMatches(e, s.q) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			select {
			case <-s.ch:
			default:
			}
			select {
			case s.ch <- e:
			default:
			}
		}
	}
}

// close 关闭全部订阅通道，流式处理器收到关闭信号后退出（服务器重载/关停）。
func (h *streamHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for s := range h.subs {
		close(s.ch)
	}
	h.subs = nil
}
