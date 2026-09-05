package webproxy

import (
	"bufio"
	"crypto/tls"
	"net"
	"sync"
	"time"
)

const maxPendingSniffs = 128

// tlsSniffListener dispatches plaintext and TLS on the same port. Sniffing runs
// outside Accept's serial path, with bounded pending connections and a deadline.
type tlsSniffListener struct {
	net.Listener
	tlsConfig *tls.Config
	once      sync.Once
	closeOnce sync.Once
	ready     chan net.Conn
	done      chan struct{}
	mu        sync.Mutex
	pending   map[net.Conn]struct{}
	err       error
}

func (l *tlsSniffListener) init() {
	l.once.Do(func() {
		l.ready = make(chan net.Conn)
		l.done = make(chan struct{})
		l.pending = make(map[net.Conn]struct{})
		go l.run()
	})
}

func (l *tlsSniffListener) run() {
	var retryDelay time.Duration
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			// Preserve net/http's retry behavior for transient accept failures (e.g.
			// descriptor pressure) instead of permanently stopping a healthy site.
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if retryDelay == 0 {
					retryDelay = 5 * time.Millisecond
				} else {
					retryDelay *= 2
				}
				if retryDelay > time.Second {
					retryDelay = time.Second
				}
				timer := time.NewTimer(retryDelay)
				select {
				case <-timer.C:
				case <-l.done:
					timer.Stop()
					return
				}
				continue
			}
			l.shutdown(err)
			return
		}
		retryDelay = 0
		l.mu.Lock()
		select {
		case <-l.done:
			l.mu.Unlock()
			conn.Close()
			return
		default:
		}
		if len(l.pending) >= maxPendingSniffs {
			l.mu.Unlock()
			conn.Close()
			continue
		}
		l.pending[conn] = struct{}{}
		l.mu.Unlock()
		go l.sniff(conn)
	}
}

func (l *tlsSniffListener) sniff(conn net.Conn) {
	defer func() { l.mu.Lock(); delete(l.pending, conn); l.mu.Unlock() }()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(conn)
	first, err := br.Peek(1)
	if err != nil {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	var result net.Conn = &sniffConn{Conn: conn, br: br}
	if first[0] == 0x16 {
		result = tls.Server(result, l.tlsConfig)
	}
	select {
	case l.ready <- result:
	case <-l.done:
		conn.Close()
	}
}

func (l *tlsSniffListener) Accept() (net.Conn, error) {
	l.init()
	select {
	case conn := <-l.ready:
		return conn, nil
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		return nil, l.err
	}
}

func (l *tlsSniffListener) shutdown(err error) {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.err = err
		close(l.done)
		for conn := range l.pending {
			conn.Close()
		}
		l.mu.Unlock()
		l.Listener.Close()
	})
}

func (l *tlsSniffListener) Close() error {
	l.init()
	l.shutdown(net.ErrClosed)
	return nil
}

// sniffConn preserves the bytes buffered by Peek.
type sniffConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *sniffConn) Read(p []byte) (int, error) { return c.br.Read(p) }
