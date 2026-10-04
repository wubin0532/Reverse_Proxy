package webproxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func TestPublishedOriginSurvivesDraftChangesUntilCloudRemoval(t *testing.T) {
	for _, kind := range []string{"rename", "delete", "switch site", "direct target"} {
		t.Run(kind, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "published") }))
			defer backend.Close()
			service, cfg, client, path := tunnelTestService(t, backend.URL, config.SubRule{})
			if err := cfg.Update(func(c *config.Config) error {
				old := c.Tunnels[0].Routes[0]
				old.AppliedHostname = old.Hostname
				old.AppliedService = "unix:" + path
				c.Tunnels[0].Routes[0] = old
				switch kind {
				case "rename":
					c.Tunnels[0].Routes[0].Hostname = "draft.example.com"
				case "delete":
					c.Tunnels[0].Routes = nil
					c.Tunnels[0].RetiredRoutes = []config.TunnelRoute{old}
				case "switch site":
					site := c.Sites[0]
					site.ID = "new-site"
					c.Sites = append(c.Sites, site)
					c.Tunnels[0].Routes[0].SiteID = site.ID
				case "direct target":
					c.Tunnels[0].Routes[0].Target = "url"
					c.Tunnels[0].Routes[0].SiteID = ""
					c.Tunnels[0].Routes[0].URL = backend.URL
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := service.Reload(); err != nil {
				t.Fatal(err)
			}
			response := tunnelRequest(t, client, "203.0.113.11", false, "/")
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 || string(body) != "published" {
				t.Fatalf("draft interrupted publication: %d %s", response.StatusCode, body)
			}
			router := chi.NewRouter()
			RegisterRoutes(router, cfg, service)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/sites/site", nil))
			if w.Code != 409 {
				t.Fatal("published site lost reference protection")
			}
			cfg.Update(func(c *config.Config) error {
				c.Tunnels[0].RetiredRoutes = nil
				for i := range c.Tunnels[0].Routes {
					c.Tunnels[0].Routes[i].AppliedHostname = ""
					c.Tunnels[0].Routes[i].AppliedService = ""
				}
				return nil
			})
			service.Reload()
			client.CloseIdleConnections()
			if kind == "rename" {
				req, _ := http.NewRequest("GET", "http://app.example.com/", nil)
				req.Header.Set("CF-Connecting-IP", "203.0.113.11")
				req.Header.Set("X-Forwarded-Proto", "https")
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != 404 {
					t.Fatal("confirmed removed hostname still authorized")
				}
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("confirmed removed socket retained")
			}
		})
	}
}
func TestRemovingTunnelOriginClosesItsWebSocketOnly(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(buf, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		buf.Flush()
		io.Copy(conn, conn)
	}))
	defer backend.Close()
	service, cfg, _, path := tunnelTestService(t, backend.URL, config.SubRule{})
	connect := func(network, address string, identity bool) (net.Conn, *bufio.Reader) {
		t.Helper()
		conn, err := net.Dial(network, address)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		headers := ""
		if identity {
			headers = "CF-Connecting-IP: 203.0.113.11\r\nX-Forwarded-Proto: https\r\n"
		}
		fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: app.example.com\r\n%sConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", headers)
		reader := bufio.NewReader(conn)
		response, err := http.ReadResponse(reader, nil)
		if err != nil || response.StatusCode != 101 {
			t.Fatalf("upgrade: %v %v", response, err)
		}
		return conn, reader
	}
	tunnel, tr := connect("unix", path, true)
	defer tunnel.Close()
	ordinary, or := connect("tcp", service.ListenAddr("site"), false)
	defer ordinary.Close()
	cfg.Update(func(c *config.Config) error { c.Tunnels[0].Routes = nil; return nil })
	service.SyncTunnelOrigins()
	fmt.Fprint(tunnel, "x")
	if _, err := tr.ReadByte(); err == nil {
		t.Fatal("revoked socket kept hijacked stream")
	}
	fmt.Fprint(ordinary, "x")
	if echo, err := or.ReadByte(); err != nil || echo != 'x' {
		t.Fatalf("ordinary stream interrupted: %v", err)
	}
}
