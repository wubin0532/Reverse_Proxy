package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

func TestCloudAPIErrorsAreRedactedWithoutBlindRetries(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				fmt.Fprint(w, `{"success":false,"errors":[{"code":1000,"message":"denied API-SECRET-CANARY"}]}`)
			}))
			defer server.Close()
			client := newCloudClient(config.TunnelAccount{Token: "API-SECRET-CANARY"})
			client.endpoint = server.URL
			err := client.do(context.Background(), "POST", "/example", map[string]string{"name": "test"}, nil)
			if err == nil || strings.Contains(err.Error(), "CANARY") || calls != 1 {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestInvalidTokenInputCannotChangeSavedCredentials(t *testing.T) {
	m, _, router := newTestManager(t)
	w := request(t, router, "PUT", "/api/tunnels/instances/instance", `{"name":"test","source":"managed","accountRef":"account","tunnelId":"`+testTunnelID+`","token":"bad\nvalue"}`)
	if w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	inst, _ := m.instance("instance")
	if inst.Token != testTunnelToken {
		t.Fatal("invalid input replaced token")
	}
}

func TestProcessLogBoundsAndRedactsAcrossChunks(t *testing.T) {
	m, _, _ := newTestManager(t)
	center, err := logcenter.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logcenter.SetDefault(center)
	defer func() { logcenter.SetDefault(nil); center.Close() }()
	inst, _ := m.instance("instance")
	writer := &processLog{manager: m, instance: inst}
	writer.Write([]byte("prefix " + inst.Token[:20]))
	writer.Write([]byte(inst.Token[20:] + "\n"))
	writer.Write(bytes.Repeat([]byte("a"), 8190))
	writer.Write([]byte(inst.Token + "\n"))
	entries, _ := center.Query(logcenter.Query{Source: "tunnel", Limit: 100})
	if len(entries) != 2 {
		t.Fatalf("entries=%d", len(entries))
	}
	for _, entry := range entries {
		if len(entry.Message) > 8192 || strings.Contains(entry.Message, "SUPER-SECRET") || strings.Contains(entry.Message, inst.Token) {
			t.Fatal("unbounded or sensitive log")
		}
	}
}
