package webproxy

import (
	"andey-proxy/internal/config"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDisabledSiteClosesHTTPStream(t *testing.T) {
	second := make(chan struct{})
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(canceled)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "1")
		w.(http.Flusher).Flush()
		select {
		case <-second:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "2")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer backend.Close()
	cfg, svc := newTestService(t)
	addSite(cfg, config.Site{ID: "review-stream", Name: "stream", Enabled: true, Listen: "127.0.0.1:0", Rules: []config.SubRule{{ID: "review-rule", Name: "reverse", Enabled: true, Type: "reverse", Backends: []string{backend.URL}}}})
	svc.Start()
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get("http://" + svc.ListenAddr("review-stream"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, 1)
	if _, err = io.ReadFull(res.Body, buf); err != nil || string(buf) != "1" {
		t.Fatal("first stream byte missing")
	}
	cfg.Lock()
	cfg.Sites[0].Enabled = false
	cfg.Unlock()
	if err = svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if status, _ := svc.SiteStatus("review-stream"); status != "stopped" {
		t.Fatal("site not stopped")
	}
	close(second)
	if _, err = io.ReadFull(res.Body, buf); err == nil {
		t.Fatal("disabled site still forwarded data")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("disabled site did not cancel the backend request")
	}
}
