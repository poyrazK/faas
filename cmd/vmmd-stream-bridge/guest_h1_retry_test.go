package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// staleKeepAliveGuest answers the first request on each connection and then
// closes the connection when the next request arrives, like a guest whose
// keep-alive timer fired while the request was in flight.
func staleKeepAliveGuest(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var served atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				reader := bufio.NewReader(conn)
				if _, err := http.ReadRequest(reader); err != nil {
					return
				}
				served.Add(1)
				_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
				_, _ = http.ReadRequest(reader) // drop the second request unanswered
			}(conn)
		}
	}()
	return ln.Addr().String(), &served
}

func TestRoundTripGuestH1RetriesBodylessRequestOnStaleConnection(t *testing.T) {
	addr, served := staleKeepAliveGuest(t)
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	send := func(method string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(context.Background(), method, "http://"+addr+"/", http.NoBody)
		return roundTripGuestH1(transport, req)
	}
	resp, err := send(http.MethodPost)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	resp, err = send(http.MethodPost)
	if err != nil {
		t.Fatalf("bodyless POST on a stale pooled connection: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := served.Load(); got != 2 {
		t.Fatalf("guest served %d requests, want 2", got)
	}
}

func TestRoundTripGuestH1NeverReplaysARequestBody(t *testing.T) {
	addr, served := staleKeepAliveGuest(t)
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	send := func() (*http.Response, error) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+addr+"/", strings.NewReader(`{"jsonrpc":"2.0"}`))
		req.GetBody = nil
		return roundTripGuestH1(transport, req)
	}
	resp, err := send()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	resp2, err := send()
	if resp2 != nil {
		_ = resp2.Body.Close()
	}
	if err == nil {
		t.Fatalf("a POST with a consumed body was replayed (served %d)", served.Load())
	}
	if got := served.Load(); got != 1 {
		t.Fatalf("guest served %d requests, want 1", got)
	}
}

func TestH1IdleConnTimeoutBelowCommonServerKeepAlive(t *testing.T) {
	// gunicorn's default keep-alive is 2 s, Node's and uvicorn's 5 s.
	if h1IdleConnTimeout >= 2*time.Second {
		t.Fatalf("h1IdleConnTimeout = %s; the guest would close idle connections first", h1IdleConnTimeout)
	}
}
