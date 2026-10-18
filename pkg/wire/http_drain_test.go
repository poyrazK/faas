package wire_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// TestHTTPDrainServesRequestThatRacedShutdown: a connection accepted before
// shutdown whose request arrives after it began is answered. Plain
// http.Server.Shutdown closes such a connection unanswered (net/http drops a
// request read while shutting down), which lost requests on every restart
// under load.
func TestHTTPDrainServesRequestThatRacedShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "served")
	})}
	drain := wire.NewHTTPDrain(server)
	go func() { _ = server.Serve(drain.Listener(listener)) }()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(50 * time.Millisecond) // accepted, request not sent yet

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopped <- drain.Shutdown(ctx, server)
	}()
	time.Sleep(100 * time.Millisecond) // shutdown has begun

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: daemon\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("request that raced shutdown got no response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "served" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// TestHTTPDrainDoesNotWaitForSilentConnections: a client that connected and
// sent nothing delays shutdown by at most the quiesce bound.
func TestHTTPDrainDoesNotWaitForSilentConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Minute, Handler: http.NotFoundHandler()}
	drain := wire.NewHTTPDrain(server)
	go func() { _ = server.Serve(drain.Listener(listener)) }()
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	_ = drain.Shutdown(ctx, server)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("shutdown waited %v for a silent connection", elapsed)
	}
}

// TestHTTPDrainServeErrReportsOnlyUnexpectedStops: a listener that fails on
// its own is still an error; one the drain closed is a clean stop.
func TestHTTPDrainServeErrReportsOnlyUnexpectedStops(t *testing.T) {
	broken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = broken.Close()
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.NotFoundHandler()}
	drain := wire.NewHTTPDrain(server)
	if err := drain.ServeErr(server.Serve(drain.Listener(broken))); err == nil {
		t.Fatal("a listener closed outside shutdown was reported as a clean stop")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server = &http.Server{ReadHeaderTimeout: time.Second, Handler: http.NotFoundHandler()}
	drain = wire.NewHTTPDrain(server)
	served := make(chan error, 1)
	go func() { served <- server.Serve(drain.Listener(listener)) }()
	time.Sleep(20 * time.Millisecond)
	if err := drain.Shutdown(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	if err := drain.ServeErr(<-served); err != nil {
		t.Fatalf("shutdown reported as a serve failure: %v", err)
	}
}
