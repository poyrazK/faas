package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/onebox-faas/faas/pkg/instancebridge"
)

// The bridge counts a request while it runs, through the real server's h2c
// stack, and an idle pooled H2C connection counts nothing (P1d): counting
// outside h2c would hold the instance busy for the connection's lifetime.
func TestBridgeCountsRequestsNotH2CConnections(t *testing.T) {
	t.Setenv("FAAS_BRIDGE_PROTOCOL", "h2c")
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	guest := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "ok")
	})}
	guest.Protocols = new(http.Protocols)
	guest.Protocols.SetUnencryptedHTTP2(true)
	guestLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = guest.Serve(guestLn) }()
	t.Cleanup(func() { _ = guest.Close() })

	counters := &instancebridge.Counters{}
	srv, _ := buildCountingServer("127.0.0.1", guestAddr(t, guestLn), time.Now().Add(time.Minute), counters)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	rt := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", ln.Addr().String())
	}}
	t.Cleanup(rt.CloseIdleConnections)

	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://bridge.invalid/slow", nil)
		resp, err := rt.RoundTrip(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		done <- err
	}()
	<-entered
	if s := counters.Snapshot(); s.Inflight != 1 {
		t.Fatalf("during the request: %+v, want 1 in flight", s)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The response is complete; the handler returns just after.
	deadline := time.Now().Add(2 * time.Second)
	for counters.Snapshot().Inflight != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s := counters.Snapshot(); s.Inflight != 0 || s.LastEnd.IsZero() {
		t.Fatalf("after the request, H2C connection still pooled: %+v, want idle with a last end", s)
	}
}
