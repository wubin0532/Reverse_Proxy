package logcenter

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxFileSize      = 1 << 20
	maxBackups       = 4
	maxMemoryEntries = 2000
)

type Entry struct {
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Source   string    `json:"source"`
	EntityID string    `json:"entityId,omitempty"`
	ClientIP string    `json:"clientIp,omitempty"`
	Message  string    `json:"message"`
}

type Query struct {
	Level, Source, Keyword, EntityID string
	Limit, Cursor                    int
	From, To                         time.Time
}

type Center struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	entries []Entry
	queue   chan []byte
	queueMu sync.Mutex
	barrier chan chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

var (
	defaultMu     sync.RWMutex
	defaultCenter *Center
)

func SetDefault(c *Center) { defaultMu.Lock(); defaultCenter = c; defaultMu.Unlock() }
func Add(source, entityID, clientIP, level, message string) {
	defaultMu.RLock()
	c := defaultCenter
	defaultMu.RUnlock()
	if c != nil {
		c.Add(Entry{Time: time.Now(), Level: level, Source: source, EntityID: entityID, ClientIP: clientIP, Message: message})
	}
}

var headerSecretPattern = regexp.MustCompile(`(?i)((?:authorization|proxy-authorization|cookie|set-cookie)\s*[:=]\s*)[^\r\n]+`)
var jsonSecretPattern = regexp.MustCompile(`(?i)("[^"]*(?:token|secret|password|passwd|api[_-]?key|access[_-]?key|auth[_-]?pass|webhook)[^"]*"\s*:\s*")[^"]*"`)
var pairSecretPattern = regexp.MustCompile(`(?i)((?:^|[?&;,\s])[^?&;,=\s]*(?:token|secret|password|passwd|api[_-]?key|access[_-]?key|auth[_-]?pass|webhook)[^?&;,=\s]*\s*[=:]\s*)[^&;,\s]+`)
var userInfoPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s:]+:[^/@\s]+@`)
var clientIPPattern = regexp.MustCompile(`(?:客户端\s*IP|client\s*ip)\s*[:：]\s*([^,，;；\s]+)`)
var entityIDPattern = regexp.MustCompile(`(?:规则|站点|任务|证书)?\s*ID\s*[:：]\s*([A-Za-z0-9._-]{1,128})`)

func New(dir string) (*Center, error) {
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(logDir, 0o700)
	c := &Center{path: filepath.Join(logDir, "andey-proxy.log"), queue: make(chan []byte, 256), barrier: make(chan chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	c.load()
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(0o600)
	c.file = f
	go c.writer()
	return c, nil
}

func (c *Center) Close() error {
	close(c.stop)
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file != nil {
		return c.file.Close()
	}
	return nil
}

// Write 使 Center 可挂到标准 log 输出；任何写盘错误都不影响调用方。
func (c *Center) Write(p []byte) (int, error) {
	msg := Redact(strings.TrimSpace(string(p)))
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	source := "system"
	if i := strings.Index(msg, "["); i >= 0 {
		if j := strings.Index(msg[i:], "]"); j > 0 {
			source = strings.ToLower(msg[i+1 : i+j])
			msg = strings.TrimSpace(msg[i+j+1:])
		}
	}
	level := "info"
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "失败") || strings.Contains(lower, "错误") || strings.Contains(lower, "error") {
		level = "error"
	} else if strings.Contains(lower, "警告") || strings.Contains(lower, "warn") {
		level = "warn"
	}
	clientIP, entityID := "", ""
	if match := clientIPPattern.FindStringSubmatch(msg); len(match) == 2 {
		clientIP = match[1]
	}
	if match := entityIDPattern.FindStringSubmatch(msg); len(match) == 2 {
		entityID = match[1]
	}
	c.Add(Entry{Time: time.Now(), Level: level, Source: source, EntityID: entityID, ClientIP: clientIP, Message: msg})
	return len(p), nil
}

func (c *Center) Add(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Level == "" {
		e.Level = "info"
	}
	if e.Source == "" {
		e.Source = "system"
	}
	e.Message = Redact(e.Message)
	if len(e.Message) > 16<<10 {
		e.Message = e.Message[:16<<10] + "…[truncated]"
	}
	c.mu.Lock()
	c.entries = append(c.entries, e)
	if len(c.entries) > maxMemoryEntries {
		c.entries = append([]Entry(nil), c.entries[len(c.entries)-maxMemoryEntries:]...)
	}
	c.mu.Unlock()
	data, _ := json.Marshal(e)
	data = append(data, '\n')
	c.queueMu.Lock()
	select {
	case c.queue <- data:
	default:
		fmt.Fprintln(os.Stderr, "[logcenter] 写入队列已满，本条仅保留在内存")
	}
	c.queueMu.Unlock()
}

func (c *Center) writer() {
	defer close(c.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]byte, 0, 64<<10)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.writeBatch(batch)
		batch = batch[:0]
	}
	for {
		select {
		case data := <-c.queue:
			batch = append(batch, data...)
			if len(batch) >= 64<<10 {
				flush()
			}
		case <-ticker.C:
			flush()
		case ack := <-c.barrier:
			flush()
			close(ack)
		case <-c.stop:
			for {
				select {
				case data := <-c.queue:
					batch = append(batch, data...)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (c *Center) writeBatch(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil {
		f, err := os.OpenFile(c.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[logcenter] 恢复磁盘日志失败: %v\n", err)
			return
		}
		_ = f.Chmod(0o600)
		c.file = f
	}
	if st, err := c.file.Stat(); err == nil && st.Size()+int64(len(data)) > maxFileSize {
		if err := c.rotateLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "[logcenter] 日志轮转失败: %v\n", err)
			return
		}
	}
	if _, err := c.file.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "[logcenter] 磁盘写入失败: %v\n", err)
	}
}

func Redact(s string) string {
	s = userInfoPattern.ReplaceAllString(s, "$1[REDACTED]@")
	s = headerSecretPattern.ReplaceAllString(s, "$1[REDACTED]")
	s = jsonSecretPattern.ReplaceAllString(s, `$1[REDACTED]"`)
	return pairSecretPattern.ReplaceAllString(s, "$1[REDACTED]")
}

func (c *Center) Query(q Query) ([]Entry, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	matched := make([]Entry, 0)
	needle := strings.ToLower(q.Keyword)
	for i := len(c.entries) - 1; i >= 0; i-- {
		e := c.entries[i]
		if q.Level != "" && e.Level != q.Level || q.Source != "" && e.Source != q.Source || q.EntityID != "" && e.EntityID != q.EntityID || !q.From.IsZero() && e.Time.Before(q.From) || !q.To.IsZero() && e.Time.After(q.To) || needle != "" && !strings.Contains(strings.ToLower(e.Message), needle) {
			continue
		}
		matched = append(matched, e)
	}
	start := q.Cursor
	if start < 0 {
		start = 0
	}
	if start >= len(matched) {
		return []Entry{}, -1
	}
	end := start + limit
	next := -1
	if end < len(matched) {
		next = end
	} else {
		end = len(matched)
	}
	return append([]Entry(nil), matched[start:end]...), next
}

func (c *Center) Clear() error {
	c.queueMu.Lock()
	for {
		select {
		case <-c.queue:
			continue
		default:
			goto drained
		}
	}
drained:
	ack := make(chan struct{})
	select {
	case c.barrier <- ack:
		<-ack
	case <-c.done:
		// writer 已退出（Close 之后）：无人接收 barrier，直接返回错误避免永久阻塞
		c.queueMu.Unlock()
		return errors.New("日志中心已关闭")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.queueMu.Unlock()
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
	for i := 0; i <= maxBackups; i++ {
		p := c.path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", c.path, i)
		}
		_ = os.Remove(p)
	}
	c.entries = nil
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_ = f.Chmod(0o600)
		c.file = f
	}
	return err
}

func (c *Center) Export(w io.Writer, q Query) error {
	c.mu.Lock()
	entries := append([]Entry(nil), c.entries...)
	c.mu.Unlock()
	enc := json.NewEncoder(w)
	needle := strings.ToLower(q.Keyword)
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if q.Level != "" && e.Level != q.Level || q.Source != "" && e.Source != q.Source || q.EntityID != "" && e.EntityID != q.EntityID || !q.From.IsZero() && e.Time.Before(q.From) || !q.To.IsZero() && e.Time.After(q.To) || needle != "" && !strings.Contains(strings.ToLower(e.Message), needle) {
			continue
		}
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func (c *Center) rotateLocked() error {
	if c.file != nil {
		if err := c.file.Close(); err != nil {
			c.file = nil
			return err
		}
		c.file = nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", c.path, maxBackups))
	for i := maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", c.path, i), fmt.Sprintf("%s.%d", c.path, i+1))
	}
	if err := os.Rename(c.path, c.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	c.file = f
	return nil
}

func (c *Center) load() {
	for i := maxBackups; i >= 0; i-- {
		p := c.path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", c.path, i)
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				e.Message = Redact(e.Message)
				c.entries = append(c.entries, e)
			}
		}
		f.Close()
	}
	if len(c.entries) > maxMemoryEntries {
		c.entries = append([]Entry(nil), c.entries[len(c.entries)-maxMemoryEntries:]...)
	}
}
