package webproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"golang.org/x/net/webdav"
)

// Every operation is relative to an os.Root. webdav.Dir follows symlinks outside
// its directory and is deliberately not used here.
type rootedDAV struct{ root *os.Root }
type davWriteKey struct{}
type davWriteState struct {
	complete          bool
	readErr, writeErr error
	copy              bool
	move              bool
}
type davUploadBody struct {
	io.ReadCloser
	state *davWriteState
}

func (b davUploadBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e == io.EOF {
		b.state.complete = true
	} else if e != nil {
		b.state.readErr = e
	}
	return n, e
}

type atomicDAVFile struct {
	*os.File
	root         *os.Root
	temp, target string
	state        *davWriteState
	closed       bool
	closeErr     error
}

func (f *atomicDAVFile) Write(p []byte) (int, error) {
	n, e := f.File.Write(p)
	if e != nil {
		f.state.writeErr = e
	}
	return n, e
}

// Hide os.File.ReadFrom: io.Copy must pass every write through our error tracker.
func (f *atomicDAVFile) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{f}, r)
}
func (f *atomicDAVFile) Close() error {
	if f.closed {
		return f.closeErr
	}
	f.closed = true
	if !f.state.complete || f.state.readErr != nil || f.state.writeErr != nil {
		_ = f.File.Close()
		_ = f.root.Remove(f.temp)
		f.closeErr = fmt.Errorf("upload was not completed")
		return f.closeErr
	}
	e := f.File.Sync()
	closeErr := f.File.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = f.root.Rename(f.temp, f.target)
	}
	if e != nil {
		_ = f.root.Remove(f.temp)
	}
	f.closeErr = e
	return e
}

type davStatusWriter struct {
	http.ResponseWriter
	state  *davWriteState
	failed bool
}

func (w *davStatusWriter) WriteHeader(status int) {
	var limit *http.MaxBytesError
	if errors.As(w.state.readErr, &limit) {
		status = 413
		w.failed = true
	}
	w.ResponseWriter.WriteHeader(status)
	if w.failed {
		_, _ = io.WriteString(w.ResponseWriter, "Upload exceeds limit\n")
	}
}
func (w *davStatusWriter) Write(p []byte) (int, error) {
	if w.failed {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
func (w *davStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func davName(name string) (string, error) {
	if strings.ContainsAny(name, "\\\x00") {
		return "", os.ErrInvalid
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".andey-dav-") {
			return "", os.ErrPermission
		}
		if part == ".." || part == "." {
			return "", os.ErrInvalid
		}
	}
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" {
		name = "."
	}
	return name, nil
}
func (d rootedDAV) Mkdir(ctx context.Context, n string, _ os.FileMode) error {
	n, e := davName(n)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return d.root.Mkdir(n, 0700)
}
func (d rootedDAV) OpenFile(ctx context.Context, n string, f int, _ os.FileMode) (webdav.File, error) {
	n, e := davName(n)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	state, _ := ctx.Value(davWriteKey{}).(*davWriteState)
	if state != nil && f&os.O_TRUNC != 0 {
		temp := filepath.Join(filepath.Dir(n), ".andey-dav-"+ids.New())
		file, e := d.root.OpenFile(temp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		return &atomicDAVFile{File: file, root: d.root, temp: temp, target: n, state: state}, nil
	}
	file, e := d.root.OpenFile(n, f, 0600)
	if e != nil {
		return nil, e
	}
	return boundedDAVFile{File: file, state: state}, nil
}
func (d rootedDAV) RemoveAll(ctx context.Context, n string) error {
	n, e := davName(n)
	if e != nil {
		return e
	}
	if n == "." {
		return os.ErrPermission
	}
	if state, _ := ctx.Value(davWriteKey{}).(*davWriteState); state != nil && (state.copy || state.move) {
		info, e := d.root.Stat(n)
		if e != nil {
			return e
		}
		if info.IsDir() {
			return os.ErrPermission
		}
		return nil
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return d.root.RemoveAll(n)
}
func (d rootedDAV) Rename(ctx context.Context, old, new string) error {
	a, e := davName(old)
	if e != nil {
		return e
	}
	b, e := davName(new)
	if e != nil {
		return e
	}
	if a == "." || b == "." {
		return os.ErrPermission
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return d.root.Rename(a, b)
}
func (d rootedDAV) Stat(ctx context.Context, n string) (os.FileInfo, error) {
	n, e := davName(n)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	return d.root.Stat(n)
}

type boundedDAVFile struct {
	*os.File
	state *davWriteState
}

func (f boundedDAVFile) Read(p []byte) (int, error) {
	n, e := f.File.Read(p)
	if f.state != nil && f.state.copy {
		if e == io.EOF {
			f.state.complete = true
		} else if e != nil {
			f.state.readErr = e
		}
	}
	return n, e
}
func (f boundedDAVFile) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, struct{ io.Reader }{f})
}

func (f boundedDAVFile) Readdir(n int) ([]os.FileInfo, error) {
	if n > 0 {
		return f.File.Readdir(min(n, 513))
	}
	entries, e := f.File.Readdir(513)
	if len(entries) > 512 {
		return nil, fmt.Errorf("目录项超过512，请使用更小的目录")
	}
	visible := entries[:0]
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".andey-dav-") {
			visible = append(visible, entry)
		}
	}
	return visible, e
}

type boundedDAVLocks struct {
	webdav.LockSystem
	mu     sync.Mutex
	expiry map[string]time.Time
}

func newDAVLocks() *boundedDAVLocks {
	return &boundedDAVLocks{LockSystem: webdav.NewMemLS(), expiry: map[string]time.Time{}}
}
func lockDuration(d time.Duration) time.Duration {
	if d <= 0 || d > 30*time.Minute {
		return 30 * time.Minute
	}
	return d
}
func (l *boundedDAVLocks) Create(now time.Time, d webdav.LockDetails) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for token, expiry := range l.expiry {
		if !expiry.After(now) {
			delete(l.expiry, token)
		}
	}
	if len(l.expiry) >= 256 {
		return "", webdav.ErrLocked
	}
	d.Duration = lockDuration(d.Duration)
	token, e := l.LockSystem.Create(now, d)
	if e == nil {
		l.expiry[token] = now.Add(d.Duration)
	}
	return token, e
}
func (l *boundedDAVLocks) Refresh(now time.Time, token string, d time.Duration) (webdav.LockDetails, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d = lockDuration(d)
	info, e := l.LockSystem.Refresh(now, token, d)
	if e == nil {
		l.expiry[token] = now.Add(d)
	}
	return info, e
}
func (l *boundedDAVLocks) Unlock(now time.Time, token string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.LockSystem.Unlock(now, token)
	if e == nil {
		delete(l.expiry, token)
	}
	return e
}

var davRequests = make(chan struct{}, 4)

func webDAVHandler(rule config.SubRule) http.Handler {
	locks := newDAVLocks()
	prefix := strings.TrimSuffix(normalizedFrontendPrefix(rule.FrontendPath), "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case davRequests <- struct{}{}:
			defer func() { <-davRequests }()
		default:
			http.Error(w, "503 WebDAV capacity reached", 503)
			return
		}
		if !rule.WebDAVWritable {
			switch r.Method {
			case "GET", "HEAD", "OPTIONS", "PROPFIND":
			default:
				w.Header().Set("Allow", "GET, HEAD, OPTIONS, PROPFIND")
				http.Error(w, "403 Read-only WebDAV", 403)
				return
			}
		} else if !rule.BasicAuth && rule.AccessGroupID == "" {
			http.Error(w, "403 Authentication required", 403)
			return
		}
		if (r.Method == "PROPFIND" || r.Method == "COPY") && r.Header.Get("Depth") == "infinity" {
			http.Error(w, "403 Recursive operation unavailable", 403)
			return
		}
		if r.Method == "PROPFIND" && r.Header.Get("Depth") == "" {
			r = r.Clone(r.Context())
			r.Header.Set("Depth", "1")
		}
		// COPY of collections is intentionally omitted; MOVE uses a rooted rename.
		if r.Method == "COPY" && r.Header.Get("Depth") != "0" {
			http.Error(w, "403 COPY requires Depth: 0", 403)
			return
		}
		root, e := os.OpenRoot(rule.RootDir)
		if e != nil {
			http.Error(w, "503 WebDAV directory unavailable", 503)
			return
		}
		defer root.Close()
		if r.Method == "COPY" {
			name, e := davName(strings.TrimPrefix(r.URL.Path, prefix))
			if e != nil {
				http.Error(w, "403 Invalid source", 403)
				return
			}
			info, e := root.Stat(name)
			if e != nil || info.IsDir() {
				http.Error(w, "403 COPY supports files only", 403)
				return
			}
		}
		if !rule.WebDAVWritable && r.Method == "OPTIONS" {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS, PROPFIND")
			w.Header().Set("DAV", "1")
			w.WriteHeader(http.StatusOK)
			return
		}
		limit := rule.MaxRequestBodyMiB
		if limit <= 0 {
			limit = 64
		}
		if limit > 10240 {
			http.Error(w, "503 Invalid upload limit", 503)
			return
		}
		if r.ContentLength > int64(limit)<<20 {
			http.Error(w, "413 Upload exceeds limit", 413)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(limit)<<20)
		if r.Method == "PUT" || r.Method == "COPY" || r.Method == "MOVE" {
			state := &davWriteState{copy: r.Method == "COPY", move: r.Method == "MOVE"}
			r = r.WithContext(context.WithValue(r.Context(), davWriteKey{}, state))
			if r.Method == "PUT" {
				r.Body = davUploadBody{r.Body, state}
			}
			w = &davStatusWriter{ResponseWriter: w, state: state}
		}
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(60 * time.Second))
		defer controller.SetReadDeadline(time.Time{})
		(&webdav.Handler{Prefix: prefix, FileSystem: rootedDAV{root}, LockSystem: locks}).ServeHTTP(w, r)
	})
}
