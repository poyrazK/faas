package gateway

// adr: 070
// spec: §4.1

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestInternalProxy_ForwardsTrailersOverH2C pins the trailer contract across
// the public→internal hop.
//
// The bug this locks down: over H2C — which is the DEFAULT for the unix
// upstream — Go's HTTP/2 transport does not pre-populate resp.Trailer. The
// keys only exist once the body has been fully read. The proxy announced
// trailers before copying the body, so it announced nothing, and the values it
// wrote afterwards were silently dropped by Go's HTTP/1.1 server because they
// had never been declared. Trailers vanished at the public edge while working
// perfectly across gatewayd-internal alone, so no single-hop test could see it.
// grpc-status travels in trailers, so this broke gRPC through the real edge.
//
// This must run against a REAL server in front of the proxy, not an
// httptest.Recorder: the defect is in what Go's server decides to put on the
// wire, and a recorder just collects whatever the handler set.
func TestInternalProxy_ForwardsTrailersOverH2C(t *testing.T) {
	for _, tc := range []struct {
		name string
		h2c  bool
	}{
		{name: "h2c upstream", h2c: true},
		{name: "http1 upstream", h2c: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Backend mirrors what gatewayd-internal does: declare the
			// trailer name, write the body, then fill the value.
			backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Trailer", "X-Guest-Trailer")
				w.Header().Add("Trailer", "Grpc-Status")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "payload")
				w.Header().Set("X-Guest-Trailer", "done")
				w.Header().Set("Grpc-Status", "0")
			})
			srv := &http.Server{Handler: backend}
			srv.Protocols = new(http.Protocols)
			srv.Protocols.SetHTTP1(true)
			srv.Protocols.SetUnencryptedHTTP2(tc.h2c)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer func() { _ = ln.Close() }()
			go func() { _ = srv.Serve(ln) }()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = srv.Shutdown(ctx)
			}()

			proxy := NewInternalReverseProxy(
				&loopbackDialer{addr: ln.Addr().String()},
				&url.URL{Scheme: "http", Host: "internal"},
				slog.Default(),
				tc.h2c,
			)
			// A real edge server, so Go decides what actually ships.
			edge := httptest.NewServer(proxy)
			defer edge.Close()

			resp, err := edge.Client().Get(edge.URL + "/trailers")
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != "payload" {
				t.Fatalf("body=%q, want %q", body, "payload")
			}
			// resp.Trailer is only valid after the body is drained.
			if got := resp.Trailer.Get("X-Guest-Trailer"); got != "done" {
				t.Errorf("X-Guest-Trailer=%q, want %q (trailers lost at the public hop)", got, "done")
			}
			if got := resp.Trailer.Get("Grpc-Status"); got != "0" {
				t.Errorf("Grpc-Status=%q, want %q — gRPC cannot complete without it", got, "0")
			}
		})
	}
}

// TestInternalProxy_AbortsTruncatedResponse pins the other half of the
// mid-stream contract: if gatewayd-internal dies after the headers are on the
// wire, the customer must see a transport error, not a short body under a
// success status.
//
// The proxy used to log the copy failure and return, which ends the response
// cleanly. A client could not distinguish "the app returned 7 bytes" from "the
// app returned 7 of 7000 bytes and the backend vanished" — silent data loss.
func TestInternalProxy_AbortsTruncatedResponse(t *testing.T) {
	// A backend that announces a long body and then dies after a few bytes.
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		// Hijack and drop the connection: the client has headers promising
		// 1000 bytes and will only ever receive 7.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	srv := &http.Server{Handler: backend}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	proxy := NewInternalReverseProxy(
		&loopbackDialer{addr: ln.Addr().String()},
		&url.URL{Scheme: "http", Host: "internal"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		false,
	)
	edge := httptest.NewServer(proxy)
	defer edge.Close()

	resp, err := edge.Client().Get(edge.URL + "/truncated")
	if err != nil {
		return // aborted before the headers landed — also a visible failure
	}
	defer func() { _ = resp.Body.Close() }()
	_, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatal("client read a truncated body as a complete success; the proxy must abort instead of returning")
	}
}
