package logcenter

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func TestStreamStopsOnLogoutAndGlobalRevocation(t *testing.T) {
	for _, mode := range []string{"logout", "password"} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer cfg.State().Close()
			hash, _ := auth.HashPassword("current-password")
			cfg.Settings.AdminUser = "admin"
			cfg.Settings.AdminPassHash = hash
			cfg.Settings.MustChangePassword = false
			center, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer center.Close()
			server := api.NewServer(cfg, false)
			server.Mount(func(r chi.Router) { RegisterRoutes(r, center, cfg) })
			ts := httptest.NewServer(server.Router())
			defer ts.Close()
			post := func(path, body string, cookie *http.Cookie) *http.Response {
				req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
				if cookie != nil {
					req.AddCookie(cookie)
				}
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			login := post("/api/login", `{"username":"admin","password":"current-password"}`, nil)
			cookie := login.Cookies()[0]
			io.Copy(io.Discard, login.Body)
			login.Body.Close()
			req, _ := http.NewRequest("GET", ts.URL+"/api/logs/stream", nil)
			req.AddCookie(cookie)
			stream, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			reader := bufio.NewReader(stream.Body)
			line, _ := reader.ReadString('\n')
			if line != ": connected\n" {
				t.Fatalf("greeting: %q", line)
			}
			reader.ReadString('\n')
			path, body := "/api/logout", `{}`
			if mode == "password" {
				path = "/api/settings/password"
				body = `{"oldPassword":"current-password","newPassword":"changed-password-2026"}`
			}
			revoked := post(path, body, cookie)
			io.Copy(io.Discard, revoked.Body)
			revoked.Body.Close()
			if revoked.StatusCode != 200 {
				t.Fatalf("revoke: %d", revoked.StatusCode)
			}
			done := make(chan error, 1)
			go func() { _, err := reader.ReadString('\n'); done <- err }()
			select {
			case err := <-done:
				if err != io.EOF {
					t.Fatalf("expected stream EOF, got %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("revoked stream still open without any new event")
			}
			center.Add(Entry{Source: "security", Message: "after revocation"})
		})
	}
}

// HTTP/2 has an active write-deadline timer: leaving a five-second deadline
// armed after a successful flush must not kill an otherwise healthy idle SSE.
func TestHTTP2StreamSurvivesIdleWriteDeadline(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.State().Close()
	hash, _ := auth.HashPassword("current-password")
	cfg.Settings.AdminUser = "admin"
	cfg.Settings.AdminPassHash = hash
	cfg.Settings.MustChangePassword = false
	center, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer center.Close()
	server := api.NewServer(cfg, true)
	server.Mount(func(r chi.Router) { RegisterRoutes(r, center, cfg) })
	ts := httptest.NewUnstartedServer(server.Router())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	client := ts.Client()
	client.Timeout = 12 * time.Second
	login, err := client.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"current-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	cookie := login.Cookies()[0]
	io.Copy(io.Discard, login.Body)
	login.Body.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/api/logs/stream", nil)
	req.AddCookie(cookie)
	stream, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.ProtoMajor != 2 {
		t.Fatal("HTTP/2 not negotiated")
	}
	reader := bufio.NewReader(stream.Body)
	reader.ReadString('\n')
	reader.ReadString('\n')
	time.Sleep(6 * time.Second)
	center.Add(Entry{Source: "security", Message: "healthy after idle"})
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: log\n" {
		t.Fatalf("idle stream failed: line=%q err=%v", line, err)
	}
}
