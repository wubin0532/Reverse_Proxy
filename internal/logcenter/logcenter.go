package logcenter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxFileSize      = 1 << 20
	maxBackups       = 4
	maxMemoryEntries = 2000
)

// 日志类型：访问日志（逐请求、量大）与系统/错误日志分库存储，
// 各自独立轮转，避免繁忙站点的访问日志冲掉错误历史。
const (
	TypeAccess = "access" // webproxy 逐请求访问日志
	TypeSystem = "system" // 错误、审计与系统日志
)

type Entry struct {
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Source   string    `json:"source"`
	EntityID string    `json:"entityId,omitempty"`
	ClientIP string    `json:"clientIp,omitempty"`
	Message  string    `json:"message"`

	// seq 进程内单调递增序号（不序列化、不进 API）。分页游标用它而不是结果集
	// 下标：下标会随新日志写入整体位移，导致翻页重复。跨类型合并查询也用它
	// 唯一定位，因此序号在 Center 级共享而不是 per-store。
	seq uint64
}

type Query struct {
	Level, Source, Keyword, EntityID string
	Type                             string // "" 全部，或 TypeAccess / TypeSystem
	Limit                            int
	Cursor                           int64 // 上一条返回条目的 seq，0 = 从头开始
	From, To                         time.Time
}

// entryRing 固定容量环形缓冲：写满后原地覆盖最旧条目，读取时按时间顺序展开。
// 早期实现用 append + 整体搬移，达到 maxMemoryEntries 后每条日志都要复制
// 约 200KB（访问日志是逐请求写入的），这里改为零搬移写入。
type entryRing struct {
	buf   []Entry
	start int // 最旧条目下标
}

func (r *entryRing) append(e Entry, limit int) {
	if limit <= 0 {
		return
	}
	if len(r.buf) < limit {
		r.buf = append(r.buf, e)
		return
	}
	r.buf[r.start] = e
	r.start++
	if r.start == len(r.buf) {
		r.start = 0
	}
}

func (r *entryRing) len() int { return len(r.buf) }

// ordered 返回按时间升序（最旧在前）的副本。
func (r *entryRing) ordered() []Entry {
	if len(r.buf) == 0 {
		return []Entry{}
	}
	out := make([]Entry, 0, len(r.buf))
	out = append(out, r.buf[r.start:]...)
	out = append(out, r.buf[:r.start]...)
	return out
}

func (r *entryRing) reset() {
	r.buf = r.buf[:0]
	r.start = 0
}

// logStore 单类型日志存储：磁盘文件 + 内存索引，独立轮转。
type logStore struct {
	path    string
	file    *os.File
	entries entryRing
}

type queuedLine struct {
	st   *logStore
	data []byte
}

type Center struct {
	mu      sync.Mutex
	system  *logStore
	access  *logStore
	queue   chan queuedLine
	queueMu sync.Mutex
	barrier chan chan struct{}
	stop    chan struct{}
	done    chan struct{}
	hub     *streamHub

	// nextSeq 由 mu 保护，为内存条目发放唯一序号（含启动时载入的历史条目）。
	nextSeq uint64
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
	c := &Center{
		system:  &logStore{path: filepath.Join(logDir, "andey-proxy.log")}, // 沿用旧文件名，兼容既有日志
		access:  &logStore{path: filepath.Join(logDir, "andey-proxy-access.log")},
		queue:   make(chan queuedLine, 256),
		barrier: make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		hub:     newStreamHub(),
	}
	// 载入的历史条目也要有唯一序号：用本地计数器连续分配，加载完成后
	// 把计数器交给 Center，避免两个库的序号碰撞（跨类型查询会合并两库）。
	seq := uint64(0)
	assign := func() uint64 { seq++; return seq }
	c.system.load(assign)
	c.access.load(assign)
	c.nextSeq = seq
	for _, st := range []*logStore{c.system, c.access} {
		f, err := os.OpenFile(st.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		_ = f.Chmod(0o600)
		st.file = f
	}
	go c.writer()
	return c, nil
}

func (c *Center) Close() error {
	c.hub.close()
	close(c.stop)
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	var firstErr error
	for _, st := range []*logStore{c.system, c.access} {
		if st.file != nil {
			if err := st.file.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
			st.file = nil
		}
	}
	return firstErr
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

// entryType 按来源分库：webproxy 逐请求条目入访问库，其余入系统库。
func entryType(e Entry) string {
	if e.Source == "webproxy" {
		return TypeAccess
	}
	return TypeSystem
}

func validLogType(t string) bool { return t == "" || t == TypeAccess || t == TypeSystem }

func (c *Center) storeOf(e Entry) *logStore {
	if entryType(e) == TypeAccess {
		return c.access
	}
	return c.system
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
	st := c.storeOf(e)
	c.mu.Lock()
	c.nextSeq++
	e.seq = c.nextSeq
	st.entries.append(e, maxMemoryEntries)
	c.mu.Unlock()
	// 唯一落盘路径：json.Marshal 保证一行一个 JSON 对象，
	// 消息内嵌的换行会被转义，不会破坏 NDJSON 结构。
	data, _ := json.Marshal(e)
	data = append(data, '\n')
	c.queueMu.Lock()
	select {
	case c.queue <- queuedLine{st: st, data: data}:
	default:
		fmt.Fprintln(os.Stderr, "[logcenter] 写入队列已满，本条仅保留在内存")
	}
	c.queueMu.Unlock()
	c.hub.publish(e)
}

func (c *Center) writer() {
	defer close(c.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	batches := map[*logStore][]byte{c.system: nil, c.access: nil}
	flush := func() {
		for st, batch := range batches {
			if len(batch) > 0 {
				c.writeBatch(st, batch)
				batches[st] = batch[:0]
			}
		}
	}
	for {
		select {
		case ql := <-c.queue:
			batches[ql.st] = append(batches[ql.st], ql.data...)
			if len(batches[ql.st]) >= 64<<10 {
				c.writeBatch(ql.st, batches[ql.st])
				batches[ql.st] = batches[ql.st][:0]
			}
		case <-ticker.C:
			flush()
		case ack := <-c.barrier:
			flush()
			close(ack)
		case <-c.stop:
			for {
				select {
				case ql := <-c.queue:
					batches[ql.st] = append(batches[ql.st], ql.data...)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (c *Center) writeBatch(st *logStore, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st.file == nil {
		f, err := os.OpenFile(st.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[logcenter] 恢复磁盘日志失败: %v\n", err)
			return
		}
		_ = f.Chmod(0o600)
		st.file = f
	}
	if sf, err := st.file.Stat(); err == nil && sf.Size()+int64(len(data)) > maxFileSize {
		if err := st.rotateLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "[logcenter] 日志轮转失败: %v\n", err)
			return
		}
	}
	if _, err := st.file.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "[logcenter] 磁盘写入失败: %v\n", err)
	}
}

// redactStems 覆盖下方四条脱敏规则可能命中的全部词干：token / secret /
// password / passwd / api|access[_-]?key / auth[_-]?pass / webhook /
// authorization / cookie / set-cookie / 带凭据的 URL。命中任一即执行完整脱敏，
// 因此词干只需"宁可多"而不可遗漏。
var redactStems = []string{"token", "secret", "password", "passwd", "key", "auth", "cookie", "webhook", "://"}

// redactCandidate 廉价预筛：只有同时出现分隔符与敏感关键词的文本才可能命中
// 脱敏规则。访问日志（"IP 路径 状态 耗时"）在这里直接放行，省去每行四次
// 正则扫描与相应的分配。
func redactCandidate(s string) bool {
	if !strings.ContainsAny(s, "=:@/") {
		return false
	}
	lower := strings.ToLower(s)
	for _, kw := range redactStems {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func Redact(s string) string {
	if !redactCandidate(s) {
		return s
	}
	s = userInfoPattern.ReplaceAllString(s, "$1[REDACTED]@")
	s = headerSecretPattern.ReplaceAllString(s, "$1[REDACTED]")
	s = jsonSecretPattern.ReplaceAllString(s, `$1[REDACTED]"`)
	return pairSecretPattern.ReplaceAllString(s, "$1[REDACTED]")
}

// matchesFilter 单条过滤（不含 Type；Type 在取数时按库分流）。
func matchesFilter(e Entry, q Query) bool {
	needle := strings.ToLower(q.Keyword)
	return !(q.Level != "" && e.Level != q.Level || q.Source != "" && e.Source != q.Source || q.EntityID != "" && e.EntityID != q.EntityID || !q.From.IsZero() && e.Time.Before(q.From) || !q.To.IsZero() && e.Time.After(q.To) || needle != "" && !strings.Contains(strings.ToLower(e.Message), needle))
}

// entryMatches 供流式订阅按完整 Query（含 Type）过滤。
func entryMatches(e Entry, q Query) bool {
	return (q.Type == "" || entryType(e) == q.Type) && matchesFilter(e, q)
}

// snapshotLocked 按 Type 取候选条目（调用方须持有 c.mu）。
func (c *Center) snapshotLocked(q Query) []Entry {
	switch q.Type {
	case TypeAccess:
		return c.access.entries.ordered()
	case TypeSystem:
		return c.system.entries.ordered()
	}
	system, access := c.system.entries.ordered(), c.access.entries.ordered()
	pool := make([]Entry, 0, len(system)+len(access))
	pool = append(pool, system...)
	pool = append(pool, access...)
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].Time.Before(pool[j].Time) })
	return pool
}

func (c *Center) Query(q Query) ([]Entry, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	pool := c.snapshotLocked(q)
	matched := make([]Entry, 0)
	for i := len(pool) - 1; i >= 0; i-- {
		if matchesFilter(pool[i], q) {
			matched = append(matched, pool[i])
		}
	}
	// 游标是上一条返回条目的 seq：用序号而不是结果集下标，翻页期间新写入的
	// 日志不会让下标整体位移（原实现第 2 页会重复上一页尾部若干条）。
	start := 0
	if q.Cursor > 0 {
		for i := range matched {
			if matched[i].seq == uint64(q.Cursor) {
				start = i + 1
				break
			}
		}
		// 找不到该条目（已被挤出内存或日志被清空）时从头开始：宁可重复不可漏。
	}
	if start >= len(matched) {
		return []Entry{}, -1
	}
	end := start + limit
	next := int64(-1)
	if end < len(matched) {
		next = int64(matched[end-1].seq)
	} else {
		end = len(matched)
	}
	return append([]Entry(nil), matched[start:end]...), next
}

// Clear 清空全部日志库。
func (c *Center) Clear() error { return c.clear("") }

// ClearType 只清空指定类型的日志库（TypeAccess / TypeSystem）。
func (c *Center) ClearType(t string) error {
	if !validLogType(t) || t == "" {
		return errors.New("日志类型无效")
	}
	return c.clear(t)
}

func (c *Center) clear(t string) error {
	c.queueMu.Lock()
	var pending []queuedLine
	for {
		select {
		case ql := <-c.queue:
			pending = append(pending, ql)
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
	targets := []*logStore{c.system, c.access}
	if t == TypeAccess {
		targets = []*logStore{c.access}
	} else if t == TypeSystem {
		targets = []*logStore{c.system}
	}
	cleared := make(map[*logStore]bool, len(targets))
	var firstErr error
	for _, st := range targets {
		cleared[st] = true
		if st.file != nil {
			_ = st.file.Close()
			st.file = nil
		}
		for i := 0; i <= maxBackups; i++ {
			p := st.path
			if i > 0 {
				p = fmt.Sprintf("%s.%d", st.path, i)
			}
			_ = os.Remove(p)
		}
		st.entries.reset()
		f, err := os.OpenFile(st.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Chmod(0o600)
			st.file = f
		} else if firstErr == nil {
			firstErr = err
		}
	}
	c.mu.Unlock()
	// 不属于清理范围的待写条目重新入队
	for _, ql := range pending {
		if !cleared[ql.st] {
			select {
			case c.queue <- ql:
			default:
			}
		}
	}
	c.queueMu.Unlock()
	return firstErr
}

func (c *Center) Export(w io.Writer, q Query) error {
	c.mu.Lock()
	pool := c.snapshotLocked(q)
	c.mu.Unlock()
	enc := json.NewEncoder(w)
	for i := len(pool) - 1; i >= 0; i-- {
		if !matchesFilter(pool[i], q) {
			continue
		}
		if err := enc.Encode(pool[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *logStore) rotateLocked() error {
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			s.file = nil
			return err
		}
		s.file = nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", s.path, maxBackups))
	for i := maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", s.path, i), fmt.Sprintf("%s.%d", s.path, i+1))
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	s.file = f
	return nil
}

// load 逐行读入历史日志。用 bufio.Reader 而非 Scanner：单行无长度上限，
// 一条超长或损坏的行只会被跳过，不会中断后续条目的加载。
func (s *logStore) load(assign func() uint64) {
	for i := maxBackups; i >= 0; i-- {
		p := s.path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", s.path, i)
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		br := bufio.NewReader(f)
		for {
			line, err := br.ReadBytes('\n')
			if line = bytes.TrimSpace(line); len(line) > 0 {
				var e Entry
				if json.Unmarshal(line, &e) == nil {
					e.Message = Redact(e.Message)
					e.seq = assign()
					s.entries.append(e, maxMemoryEntries)
				}
			}
			if err != nil {
				break
			}
		}
		f.Close()
	}
}
