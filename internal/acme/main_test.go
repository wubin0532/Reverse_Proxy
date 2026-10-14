package acme

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
)

// Configuration tests may trigger asynchronous issuance. Keep those requests
// inside local fixtures so unit tests cannot create real accounts or orders.
func TestMain(m *testing.M) {
	original := http.DefaultTransport
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("ACME unit tests only permit loopback HTTP fixtures")
		}
		return dial(ctx, network, address)
	}
	http.DefaultTransport = transport
	code := m.Run()
	transport.CloseIdleConnections()
	http.DefaultTransport = original
	os.Exit(code)
}
