package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestGuestTransportPoolReusesTransportsPerPort(t *testing.T) {
	pool := newGuestTransportPool("127.0.0.1")
	if got := pool.h2c(8080); got != pool.h2c(8080) {
		t.Fatal("H2C transport was not reused for the same guest port")
	}
	if got := pool.h1(8080); got != pool.h1(8080) {
		t.Fatal("H1 transport was not reused for the same guest port")
	}
	if got := len(pool.entries); got != 1 {
		t.Fatalf("pool entries = %d, want one shared port entry", got)
	}
	if pool.entries[8080].h1 == nil || pool.entries[8080].h2c == nil {
		t.Fatal("shared port entry did not retain both protocol transports")
	}

	pool.closeIdleConnections()
	if got := len(pool.entries); got != 0 {
		t.Fatalf("pool entries after close = %d, want zero", got)
	}
}

func TestGuestTransportPoolBoundsPortEntries(t *testing.T) {
	pool := newGuestTransportPool("127.0.0.1")
	now := time.Unix(100, 0)
	pool.now = func() time.Time { return now }
	for port := uint16(1); port <= guestTransportMaxPorts+1; port++ {
		pool.h1(port)
		now = now.Add(time.Second)
	}
	if got := len(pool.entries); got != guestTransportMaxPorts {
		t.Fatalf("pool entries = %d, want bounded at %d", got, guestTransportMaxPorts)
	}
	if _, ok := pool.entries[1]; ok {
		t.Fatal("oldest port entry was not evicted after reaching the bound")
	}
}

func TestGuestTransportPoolRetriesAndPrewarmsH2CConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var connections atomic.Int32
	var requests atomic.Int32
	server := &http2.Server{}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			if connections.Add(1) == 1 {
				// A restored guest can briefly accept TCP before its H2 server is
				// ready. Prove prewarming survives that first reset.
				_ = conn.Close()
				continue
			}
			go server.ServeConn(conn, &http2.ServeConnOpts{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, "ok")
			})})
		}
	}()

	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	pool := newGuestTransportPool("127.0.0.1")
	defer pool.closeIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pool.prewarmH2C(ctx, uint16(port)); err != nil {
		t.Fatalf("prewarmH2C: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("guest requests during protocol prewarm = %d, want zero", got)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+portText+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := pool.h2c(uint16(port)).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("guest TCP connections = %d, want one reset plus the reused prewarmed connection", got)
	}
}

func TestGuestTransportPoolFirstRequestWaitsForH2CPrewarm(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	firstAccepted := make(chan struct{})
	serveFirst := make(chan struct{})
	var connections atomic.Int32
	server := &http2.Server{}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			if connections.Add(1) == 1 {
				close(firstAccepted)
				go func() {
					<-serveFirst
					server.ServeConn(conn, &http2.ServeConnOpts{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = io.WriteString(w, "ok")
					})})
				}()
				continue
			}
			go server.ServeConn(conn, &http2.ServeConnOpts{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "ok")
			})})
		}
	}()

	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	pool := newGuestTransportPool("127.0.0.1")
	defer pool.closeIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	prewarmResult := make(chan error, 1)
	go func() { prewarmResult <- pool.prewarmH2C(ctx, uint16(port)) }()
	select {
	case <-firstAccepted:
	case <-ctx.Done():
		t.Fatalf("waiting for prewarm connection: %v", ctx.Err())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+portText+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	type roundTripResult struct {
		status int
		err    error
	}
	requestResult := make(chan roundTripResult, 1)
	go func() {
		resp, roundTripErr := pool.h2c(uint16(port)).RoundTrip(req)
		if roundTripErr != nil {
			requestResult <- roundTripResult{err: roundTripErr}
			return
		}
		defer resp.Body.Close()
		_, roundTripErr = io.Copy(io.Discard, resp.Body)
		requestResult <- roundTripResult{status: resp.StatusCode, err: roundTripErr}
	}()

	// Keep the prewarm handshake blocked long enough for the request to enter
	// RoundTrip. It must wait instead of dialing a second guest connection.
	var result roundTripResult
	haveResult := false
	select {
	case result = <-requestResult:
		haveResult = true
		t.Errorf("RoundTrip completed before prewarm: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := connections.Load(); got != 1 {
		t.Errorf("guest TCP connections while prewarm was in flight = %d, want one", got)
	}
	close(serveFirst)

	if err := <-prewarmResult; err != nil {
		t.Fatalf("prewarmH2C: %v", err)
	}
	if !haveResult {
		select {
		case result = <-requestResult:
		case <-ctx.Done():
			t.Fatalf("waiting for first request: %v", ctx.Err())
		}
	}
	if result.err != nil {
		t.Fatalf("RoundTrip: %v", result.err)
	}
	if result.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", result.status)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("guest TCP connections = %d, want the reused prewarmed connection", got)
	}
}

func TestPersistentH1HandlerReusesGuestConnection(t *testing.T) {
	var connections atomic.Int32
	guest := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "2")
		_, _ = io.WriteString(w, "ok")
	}))
	guest.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	guest.Start()
	defer guest.Close()

	_, portText, err := net.SplitHostPort(guest.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	pool := newGuestTransportPool("127.0.0.1")
	defer pool.closeIdleConnections()
	handler := newHandlerWithPool("127.0.0.1", uint16(port), time.Now().Add(time.Minute), pool)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://unix/", nil)
		req.Header.Set(bridgeRequestMarkerHeader, "1")
		req.Header.Set(bridgeRequestProtocolHeader, "h1")
		req.Header.Set(bridgeRequestPortHeader, portText)
		req.Header.Set(bridgeRequestHostHeader, "guest.test")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i, rr.Code)
		}
		if got := rr.Body.String(); got != "ok" {
			t.Fatalf("request %d body = %q, want ok", i, got)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("guest TCP connections = %d, want one keep-alive connection", got)
	}
}
