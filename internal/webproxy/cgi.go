package webproxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

var cgiProcesses = make(chan struct{}, 4)

const cgiOutputLimit = 8 << 20

// A request always runs the configured executable, never a filename or shell
// expression supplied in the URL. Linux executes an inherited rooted descriptor.
// macOS does not permit exec on /dev/fd; its development fallback verifies the
// canonical path and file identity. CGI directories must contain trusted files.
func cgiHandler(rule config.SubRule) http.Handler {
	directory := bindServiceDirectory(rule.RootDir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rule.BasicAuth && rule.AccessGroupID == "" {
			http.Error(w, "403 CGI authentication required", 403)
			return
		}
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			http.Error(w, "501 CGI unavailable on this platform", 501)
			return
		}
		select {
		case cgiProcesses <- struct{}{}:
			defer func() { <-cgiProcesses }()
		default:
			http.Error(w, "503 CGI capacity reached", 503)
			return
		}
		if r.ContentLength < 0 {
			http.Error(w, "411 CGI requires Content-Length", 411)
			return
		}
		if r.ContentLength > 8<<20 {
			http.Error(w, "413 CGI body exceeds 8 MiB", 413)
			return
		}
		root, e := directory.open()
		if e != nil {
			http.Error(w, "503 CGI directory unavailable", 503)
			return
		}
		defer root.Close()
		if !filepath.IsLocal(rule.CGIEntry) {
			http.Error(w, "503 CGI entry unavailable", 503)
			return
		}
		executable, e := root.Open(rule.CGIEntry)
		if e != nil {
			http.Error(w, "503 CGI entry unavailable", 503)
			return
		}
		defer executable.Close()
		info, e := executable.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			http.Error(w, "503 CGI entry must be an executable file", 503)
			return
		}
		seconds := rule.CGITimeoutSeconds
		if seconds <= 0 {
			seconds = 15
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
		defer cancel()
		fdPath := "/proc/self/fd/3"
		if runtime.GOOS == "darwin" {
			base, err := filepath.EvalSymlinks(rule.RootDir)
			if err != nil {
				http.Error(w, "503 CGI directory unavailable", 503)
				return
			}
			resolved, err := filepath.EvalSymlinks(filepath.Join(base, rule.CGIEntry))
			relative, relErr := filepath.Rel(base, resolved)
			current, statErr := os.Stat(resolved)
			if err != nil || relErr != nil || !filepath.IsLocal(relative) || statErr != nil || !os.SameFile(info, current) {
				http.Error(w, "503 CGI entry unavailable", 503)
				return
			}
			fdPath = resolved
		}
		cmd := exec.CommandContext(ctx, fdPath)
		cmd.Args = []string{rule.CGIEntry}
		cmd.ExtraFiles = []*os.File{executable}
		cmd.Dir = rule.RootDir
		if runtime.GOOS == "linux" {
			cwd, err := root.Open(".")
			if err != nil {
				http.Error(w, "503 CGI directory unavailable", 503)
				return
			}
			defer cwd.Close()
			// Go changes directory before remapping ExtraFiles in the child,
			// so use the inherited parent descriptor number here.
			cmd.Dir = fmt.Sprintf("/proc/self/fd/%d", cwd.Fd())
		}
		cmd.Env = cgiEnvironment(r, rule)
		cmd.Stdin = http.MaxBytesReader(w, r.Body, 8<<20)
		cmd.Stderr = io.Discard
		cmd.WaitDelay = time.Second
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(time.Duration(seconds) * time.Second))
		defer controller.SetReadDeadline(time.Time{})
		stdout, e := cmd.StdoutPipe()
		if e != nil {
			http.Error(w, "502 CGI launch failed", 502)
			return
		}
		if e = cmd.Start(); e != nil {
			log.Printf("[webproxy] CGI启动失败: %v", e)
			http.Error(w, "502 CGI launch failed", 502)
			return
		}
		// Closing the upload on cancellation releases an exec stdin-copy goroutine.
		stopped := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				r.Body.Close()
			case <-stopped:
			}
		}()
		defer close(stopped)
		output, readErr := io.ReadAll(io.LimitReader(stdout, cgiOutputLimit+1))
		if len(output) > cgiOutputLimit || readErr != nil {
			_ = cmd.Process.Kill()
		}
		_ = stdout.Close()
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			http.Error(w, "504 CGI execution timed out or cancelled", 504)
			return
		}
		if readErr != nil || waitErr != nil || len(output) > cgiOutputLimit {
			http.Error(w, "502 CGI output failed or exceeds 8 MiB", 502)
			return
		}
		headerEnd := bytes.Index(output, []byte("\r\n\r\n"))
		separator := 4
		if headerEnd < 0 {
			headerEnd = bytes.Index(output, []byte("\n\n"))
			separator = 2
		}
		if headerEnd < 0 || headerEnd > 65536 {
			http.Error(w, "502 Invalid CGI response headers", 502)
			return
		}
		headerBytes := append(append([]byte(nil), output[:headerEnd]...), []byte("\r\n\r\n")...)
		headers, e := textproto.NewReader(bufio.NewReader(bytes.NewReader(headerBytes))).ReadMIMEHeader()
		if e != nil {
			http.Error(w, "502 Invalid CGI response headers", 502)
			return
		}
		status := 200
		if value := headers.Get("Status"); value != "" {
			fields := strings.Fields(value)
			code, err := strconv.Atoi(fields[0])
			if err != nil || code < 200 || code > 599 {
				http.Error(w, "502 Invalid CGI status", 502)
				return
			}
			status = code
		} else if headers.Get("Location") != "" {
			status = 302
		}
		if headers.Get("Content-Type") == "" && headers.Get("Location") == "" {
			http.Error(w, "502 CGI response requires Content-Type", 502)
			return
		}
		response := &http.Response{Header: http.Header(headers), Request: r.WithContext(context.WithValue(r.Context(), originalRequestKey{}, r))}
		for _, key := range []string{"Status", "Connection", "Transfer-Encoding", "Content-Length", "Upgrade", "Trailer", "Proxy-Authenticate", "Proxy-Authorization", "Keep-Alive", "Te"} {
			response.Header.Del(key)
		}
		responseRule := rule
		responseRule.RewriteLocation = false
		rewriteProxyResponse(response, &url.URL{}, responseRule)
		for key, values := range response.Header {
			w.Header().Del(key)
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(status)
		if r.Method != "HEAD" {
			_, _ = w.Write(output[headerEnd+separator:])
		}
	})
}
func cgiEnvironment(r *http.Request, rule config.SubRule) []string {
	prefix := strings.TrimSuffix(normalizedFrontendPrefix(rule.FrontendPath), "/")
	port := "80"
	if requestScheme(r) == "https" {
		port = "443"
	}
	if _, value, err := net.SplitHostPort(r.Host); err == nil {
		port = value
	}
	env := []string{"PATH=/usr/bin:/bin", "LANG=C", "GATEWAY_INTERFACE=CGI/1.1", "SERVER_SOFTWARE=andey-proxy", "SERVER_PROTOCOL=" + r.Proto, "REQUEST_METHOD=" + r.Method, "REQUEST_URI=" + r.URL.RequestURI(), "QUERY_STRING=" + r.URL.RawQuery, "SCRIPT_NAME=" + prefix, "PATH_INFO=" + strings.TrimPrefix(r.URL.Path, prefix), "SERVER_NAME=" + hostOnly(r.Host), "REMOTE_ADDR=" + clientIP(r), "CONTENT_LENGTH=" + fmt.Sprint(max(r.ContentLength, 0)), "CONTENT_TYPE=" + r.Header.Get("Content-Type")}
	env = append(env, "HTTP_HOST="+r.Host, "SERVER_PORT="+port, "SCRIPT_FILENAME="+filepath.Join(rule.RootDir, rule.CGIEntry), "DOCUMENT_ROOT="+rule.RootDir)
	headers := r.Header.Clone()
	stripCookieHeader(headers, api.TokenCookie)
	stripAccessCookies(headers)
	headers.Del("Authorization")
	for _, key := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "CF-Connecting-IP", "True-Client-IP"} {
		headers.Del(key)
	}
	if rule.ProxyHeadersEnabled() {
		headers.Set("X-Forwarded-For", clientIP(r))
		headers.Set("X-Forwarded-Host", r.Host)
		headers.Set("X-Forwarded-Proto", requestScheme(r))
	}
	for key, values := range headers {
		if strings.EqualFold(key, "Proxy") {
			continue
		}
		name := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		env = append(env, "HTTP_"+name+"="+strings.Join(values, ", "))
	}
	if requestScheme(r) == "https" {
		env = append(env, "HTTPS=on")
	}
	return env
}
