package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/notify"
)

type worker struct {
	instance    config.TunnelInstance
	cancel      context.CancelFunc
	done        chan struct{}
	mu          sync.Mutex
	status      Status
	connectorID string
}

func (w *worker) update(process string, ready bool, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status.Process, w.status.Ready, w.status.Error = process, ready, message
}

func (m *Manager) supervise(ctx context.Context, w *worker) {
	delay := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := m.runProcess(ctx, w)
		if ctx.Err() != nil {
			break
		}
		message := "cloudflared 已退出，将自动重试"
		if err != nil {
			message = err.Error()
		}
		w.update("retrying", false, message)
		logcenter.Add("tunnel", w.instance.ID, "", "warn", message)
		if time.Since(started) > time.Minute {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
		}
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
	w.update("stopped", false, "")
}

func (m *Manager) runProcess(ctx context.Context, w *worker) error {
	if err := validateInstance(&w.instance); err != nil {
		return err
	}
	if err := m.validateCredentials(&w.instance); err != nil {
		return err
	}
	rt := m.runtime()
	if !rt.Compatible {
		return fmt.Errorf("%s", rt.Message)
	}
	dir, err := os.MkdirTemp("", "andey-tunnel-")
	if err != nil {
		return fmt.Errorf("无法创建隧道凭据临时目录")
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte(w.instance.Token), 0o600); err != nil {
		return fmt.Errorf("无法写入隧道凭据临时文件")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("无法分配隧道状态端口")
	}
	metrics := ln.Addr().String()
	_ = ln.Close()
	args := []string{"tunnel", "--no-autoupdate", "--metrics", metrics, "--loglevel", "info", "--output", "json", "--protocol", w.instance.Protocol, "--edge-ip-version", w.instance.IPVersion, "--grace-period", "5s", "run", "--token-file", path}
	cmd := exec.Command(rt.Path, args...)
	prepareProcess(cmd)
	writer := &processLog{manager: m, instance: w.instance}
	cmd.Stdout, cmd.Stderr = writer, writer
	// Do not inherit API/tunnel tokens supplied to the parent through its environment.
	for _, entry := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if strings.Contains(key, "TOKEN") || strings.Contains(key, "SECRET") || strings.HasPrefix(key, "TUNNEL_") || key == "NO_AUTOUPDATE" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cloudflared 启动失败")
	}
	w.update("running", false, "")
	done := make(chan error, 1)
	go func() { err := cmd.Wait(); writer.flush(); done <- err }()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	wasReady := false
	for {
		select {
		case err := <-done:
			if wasReady {
				notify.Publish(notify.Event{Type: "tunnel.disconnected", Level: "warn", Entity: w.instance.Name, Message: "隧道连接中断"})
			}
			if err != nil {
				return fmt.Errorf("cloudflared 异常退出")
			}
			return nil
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
			timer := time.NewTimer(7 * time.Second)
			select {
			case <-done:
				timer.Stop()
			case <-timer.C:
				_ = cmd.Process.Kill()
				<-done
			}
			return nil
		case <-ticker.C:
			ready := false
			resp, err := client.Get("http://" + metrics + "/ready")
			if err == nil {
				var info struct {
					ConnectorID string `json:"connectorId"`
				}
				_ = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4096)).Decode(&info)
				resp.Body.Close()
				ready = resp.StatusCode == http.StatusOK
				w.mu.Lock()
				w.connectorID = info.ConnectorID
				w.mu.Unlock()
			}
			w.update("running", ready, "")
			if ready != wasReady {
				typ, level, message := "tunnel.disconnected", "warn", "隧道连接中断"
				if ready {
					typ, level, message = "tunnel.connected", "info", "隧道连接已就绪"
				}
				notify.Publish(notify.Event{Type: typ, Level: level, Entity: w.instance.Name, Message: message})
				wasReady = ready
			}
		}
	}
}

// Bound each line and discard its remainder while continuing to drain output.
type processLog struct {
	mu        sync.Mutex
	manager   *Manager
	instance  config.TunnelInstance
	line      []byte
	truncated bool
}

func (l *processLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		remaining := 8192 - len(l.line)
		if len(part) > remaining {
			part = part[:remaining]
			l.truncated = true
		}
		l.line = append(l.line, part...)
		if end < 0 {
			break
		}
		l.emit()
		p = p[end+1:]
	}
	return n, nil
}
func (l *processLog) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.line) > 0 {
		l.emit()
	}
}
func (l *processLog) emit() {
	message := string(l.line)
	// If truncated, discard the entire line: a secret may straddle the boundary.
	if l.truncated {
		message = "cloudflared 日志行过长，已丢弃"
	}
	if l.instance.Token != "" {
		message = strings.ReplaceAll(message, l.instance.Token, "[REDACTED]")
	}
	l.manager.cfg.RLock()
	for _, account := range l.manager.cfg.TunnelAccounts {
		if account.Token != "" {
			message = strings.ReplaceAll(message, account.Token, "[REDACTED]")
		}
	}
	l.manager.cfg.RUnlock()
	level := "info"
	var entry struct {
		Level string `json:"level"`
	}
	if json.Unmarshal([]byte(message), &entry) == nil && (entry.Level == "error" || entry.Level == "warn") {
		level = entry.Level
	}
	logcenter.Add("tunnel", l.instance.ID, "", level, logcenter.Redact(message))
	l.line = l.line[:0]
	l.truncated = false
}
