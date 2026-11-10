package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestInternalProxy_NegotiatesH2C pins the issue #675 contract:
// when useH2C is true, the proxy negotiates HTTP/2 prior knowledge
// against an H2C-capable backend (Go 1.24+ Protocols.SetUnencryptedHTTP2).
// Asserts r.Proto == "HTTP/2.0" on the inbound server-side request.
func TestInternalProxy_NegotiatesH2C(t *testing.T) {
	var (
		mu        sync.Mutex
		gotProto  string
		gotMethod string
		gotPath   string
	)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotProto = r.Proto
		gotMethod = r.Method
		gotPath = r.URL.Path
		mu.Unlock()
		w.Header().Set("X-Backend", "h2c")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	srv := &http.Server{Handler: backend}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	dialer := &loopbackDialer{addr: ln.Addr().String()}
	target := &url.URL{Scheme: "http", Host: "internal"}
	proxy := NewInternalReverseProxy(dialer, target, slog.Default(), true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/synthesize", nil)
	proxy.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d (body=%q)", resp.StatusCode, rec.Body.String())
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("body: %q", string(body))
	}
	if resp.Header.Get("X-Backend") != "h2c" {
		t.Fatalf("missing X-Backend header")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotProto != "HTTP/2.0" {
		t.Fatalf("backend saw r.Proto=%q; want HTTP/2.0", gotProto)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("backend saw method=%q; want GET", gotMethod)
	}
	if gotPath != "/v1/synthesize" {
		t.Fatalf("backend saw path=%q; want /v1/synthesize", gotPath)
	}
}

// TestInternalProxy_NegotiatesH2C_OverUnixSocket is the production-wire
// version. The TCP-loopback variant does not exercise unix-socket-specific
// behaviour that the production /run/faas/gatewayd-internal.sock depends
// on. This test uses NewUnixSocketDialer (the production dialer shape
// from cmd/gatewayd-public/main.go) against a temp /tmp/foo.sock mirroring
// the production path.
func TestInternalProxy_NegotiatesH2C_OverUnixSocket(t *testing.T) {
	var (
		mu       sync.Mutex
		gotProto string
		gotPath  string
	)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotProto = r.Proto
		gotPath = r.URL.Path
		mu.Unlock()
		w.Header().Set("X-Backend", "h2c-unix")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	srv := &http.Server{Handler: backend}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	sock := "/tmp/" + strings.ReplaceAll(t.Name(), "/", "_") + ".sock"
	_ = os.Remove(sock)
	ul, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sock, err)
	}
	defer func() {
		_ = ul.Close()
		_ = os.Remove(sock)
	}()
	go func() { _ = srv.Serve(ul) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	dialer := NewUnixSocketDialer(sock)
	target := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	proxy := NewInternalReverseProxy(dialer, target, slog.Default(), true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/coolapp", nil)
	proxy.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d (body=%q)", resp.StatusCode, rec.Body.String())
	}
	if resp.Header.Get("X-Backend") != "h2c-unix" {
		t.Fatalf("missing X-Backend header -- proxy did not reach backend through H2C transport over unix socket")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotProto != "HTTP/2.0" {
		t.Fatalf("backend saw r.Proto=%q; want HTTP/2.0 -- H2C did not negotiate over the unix socket", gotProto)
	}
	if gotPath != "/v1/apps/coolapp" {
		t.Fatalf("backend saw path=%q; want /v1/apps/coolapp", gotPath)
	}
}

// TestInternalProxy_HTTP11Fallback asserts that when useH2C is false,
// the proxy uses the legacy HTTP/1.1 transport against an H1-only
// backend. Acts as a regression guard for the FAAS_INTERNAL_H2C=false
// rollback path.
func TestInternalProxy_HTTP11Fallback(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "h1")
	})
	srv := httptest.NewServer(backend)
	defer srv.Close()

	dialer := &loopbackDialer{addr: strings.TrimPrefix(srv.URL, "http://")}
	target := &url.URL{Scheme: "http", Host: "internal"}
	proxy := NewInternalReverseProxy(dialer, target, slog.Default(), false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/synthesize", nil)
	proxy.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "h1") {
		t.Fatalf("body: %q", string(body))
	}
}

// TestInternalProxy_H2CMultiplexesConcurrentStreams pins the H2C
// multiplex behaviour: N concurrent requests against one proxy-dialer
// combo must each reach the backend (H2 does not accidentally serialise
// them on one connection). The full WakeGate coalescing guarantee is
// exercised at the Handler layer by
// TestConcurrentColdRequestsCoalesceToOneWake in
// pkg/gateway/handler_test.go:412; this test is the proxy-level
// companion confirming H2C does not starve under N concurrent requests.
//
// Asserting wakeCount == N proves the transport multiplexed N concurrent
// RoundTrip calls. If H2C were serialising (or the dialer were
// serialising), wakeCount would be 1.
func TestInternalProxy_H2CMultiplexesConcurrentStreams(t *testing.T) {
	var wakeCount atomic.Int32
	dispatcher := func(w http.ResponseWriter, r *http.Request) {
		wakeCount.Add(1)
		// Hold the wake long enough that siblings have time to
		// attempt concurrent in-flight requests. Not a load-bearing
		// check on its own -- only the wakeCount == N assertion is.
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}

	backend := &http.Server{Handler: http.HandlerFunc(dispatcher)}
	backend.Protocols = new(http.Protocols)
	backend.Protocols.SetHTTP1(true)
	backend.Protocols.SetUnencryptedHTTP2(true)

	sock := "/tmp/" + strings.ReplaceAll(t.Name(), "/", "_") + ".sock"
	_ = os.Remove(sock)
	ul, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sock, err)
	}
	defer func() {
		_ = ul.Close()
		_ = os.Remove(sock)
	}()
	go func() { _ = backend.Serve(ul) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = backend.Shutdown(ctx)
	}()

	proxy := NewInternalReverseProxy(
		NewUnixSocketDialer(sock),
		&url.URL{Scheme: "http", Host: "gatewayd-internal"},
		slog.Default(),
		true, // useH2C
	)

	const N = 20
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/synthesize", nil)
			proxy.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	if got := wakeCount.Load(); got != int32(N) {
		t.Fatalf("backend saw %d wake calls; want %d -- H2C did not multiplex concurrent requests on the unix socket", got, N)
	}
}

type loopbackDialer struct{ addr string }

func (d *loopbackDialer) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.addr)
}

// TestInternalProxy_StreamingStaysHTTP11 pins the issue #690
// contract: an H2C-wrapped SSE-shaped streaming response from the
// upstream arrives at the inbound writer with per-chunk flush
// timing — not buffered up to the io.Copy threshold. The fix
// landed in PR #713 (commit 0cf42fe7, copyResponseBody Flush);
// this test pins the contract so a future io.Copy refactor
// doesn't regress it.
//
// SSE-shape: emit 20 chunks of 64 bytes spaced 10 ms apart.
// Total ~ 200 ms. Assert the inbound writer sees the full body
// (one chunk per upstream Flush) and the response tears down
// cleanly under H2C transport.
//
// httptest with EnableHTTP2=true forces TLS+ALPN, which doesn't
// match the production H2C path (plain TCP + H2C prior knowledge).
// Replicate the same listener shape as TestInternalProxy_NegotiatesH2C
// — net.Listen("tcp") + http.Server.Protocols.SetUnencryptedHTTP2(true)
// — so the proxy negotiates H2C the way it does against
// gatewayd-internal in production.
func TestInternalProxy_StreamingStaysHTTP11(t *testing.T) {
	const totalChunks = 20
	const chunkBytes = 64
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < totalChunks; i++ {
			chunk := strings.Repeat("x", chunkBytes)
			_, _ = io.WriteString(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	srv := &http.Server{Handler: backend}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	dialer := &loopbackDialer{addr: ln.Addr().String()}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: ln.Addr().String()}, slog.Default(), true)

	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(reqCtx)

	rec := httptest.NewRecorder()
	start := time.Now()
	p.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.Bytes()
	if got := len(body); got != totalChunks*chunkBytes {
		t.Errorf("body bytes = %d, want %d (full streaming response must reach inbound)", got, totalChunks*chunkBytes)
	}
	// Streaming must NOT be cut by DialTimeout (10 s default) — total
	// streaming time is ~ 200 ms; allow generous slack for CI noise.
	if elapsed > 4*time.Second {
		t.Errorf("elapsed = %v, want < 4s (no premature cut)", elapsed)
	}
}

// BenchmarkSmallResponseOverHTTP2 serves a 512-byte body through
// copyResponseBodyWithActivity over HTTP/2 with TLS, the edge's public
// shape. With a declared length the final chunk is not flushed on its own,
// so the body and the end of the stream leave in one write.
func BenchmarkSmallResponseOverHTTP2(b *testing.B) {
	body := strings.Repeat("x", 512)
	for _, bc := range []struct {
		name   string
		length int64
	}{
		{"declared_length", int64(len(body))},
		{"unknown_length", -1},
	} {
		b.Run(bc.name, func(b *testing.B) {
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = copyResponseBodyWithActivity(r.Context(), w, io.NopCloser(strings.NewReader(body)), bc.length, nil)
			}))
			srv.EnableHTTP2 = true
			srv.StartTLS()
			defer srv.Close()
			client := srv.Client()
			b.ReportAllocs()
			for b.Loop() {
				resp, err := client.Get(srv.URL)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.ProtoMajor != 2 {
					b.Fatalf("proto %s, want HTTP/2", resp.Proto)
				}
			}
		})
	}
}
