package main

// adr: 989 — guest-init relays app bytes to the host without reading them.

import (
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestOutboundRelayEndpointMatchesAPI keeps guest-init's relay address and
// the one `gregale add ai` writes into app settings the same (ADR
// 20261027132936).
func TestOutboundRelayEndpointMatchesAPI(t *testing.T) {
	if OutboundRelayEndpoint != api.OutboundRelayURL {
		t.Fatalf("guest-init relay %q, api.OutboundRelayURL %q", OutboundRelayEndpoint, api.OutboundRelayURL)
	}
}

func TestServeOutboundRelayCopiesBothDirections(t *testing.T) {
	host, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close() }()
	received := make(chan string, 1)
	go func() {
		conn, err := host.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		request, _ := io.ReadAll(conn)
		received <- string(request)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\ndata: hi\n\n")
	}()
	relay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = relay.Close() }()
	go serveOutboundRelay(relay, func() (relayHostConn, error) {
		conn, err := net.Dial("tcp", host.Addr().String())
		if err != nil {
			return nil, err
		}
		return conn.(*net.TCPConn), nil
	}, slog.Default())

	app, err := net.Dial("tcp", relay.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	_ = app.SetDeadline(time.Now().Add(5 * time.Second))
	request := "POST /i/llm/v1/chat/completions HTTP/1.1\r\nHost: 127.0.0.1:2774\r\n\r\n"
	if _, err := io.WriteString(app, request); err != nil {
		t.Fatal(err)
	}
	_ = app.(*net.TCPConn).CloseWrite()
	response, err := io.ReadAll(app)
	if err != nil || !strings.HasSuffix(string(response), "data: hi\n\n") {
		t.Fatalf("app read %q, %v", response, err)
	}
	if got := <-received; got != request {
		t.Fatalf("host received %q", got)
	}
}

func TestServeOutboundRelayClosesWhenTheHostIsUnavailable(t *testing.T) {
	relay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = relay.Close() }()
	go serveOutboundRelay(relay, func() (relayHostConn, error) { return nil, io.ErrClosedPipe }, slog.Default())
	app, err := net.Dial("tcp", relay.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	_ = app.SetDeadline(time.Now().Add(5 * time.Second))
	if n, err := app.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("read = %d, %v; want a closed connection", n, err)
	}
}

func TestStampOutboundRelayEnv(t *testing.T) {
	env := StampOutboundRelayEnv([]string{"GREGALE_OUTBOUND_URL=http://evil.example"})
	if env[len(env)-1] != "GREGALE_OUTBOUND_URL=http://127.0.0.1:2774" {
		t.Fatalf("platform URL not stamped last: %v", env)
	}
}
