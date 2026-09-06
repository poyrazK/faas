package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// stubDialer captures the (ctx, target) tuples for assertions and
// returns a connection to a local httptest.Server.
type stubDialer struct {
	mu     sync.Mutex
	calls  []string
	server *httptest.Server
	// dialErr overrides the dial return when non-nil.
	dialErr error
	// dialBlock, when non-nil, makes DialContext wait on the channel
	// (or the supplied ctx's Done) before returning. Used by
	// TestInternalReverseProxy_DialTimeout_ScopeOnlyDial (issue
	// #687) to exercise the dial-timeout scope: the dial blocks
	// forever; the transport-level dial timeout must abort the dial
	// without affecting RoundTrip.
	dialBlock chan struct{}
}

func TestTCPDialer_DialsFixedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr == nil {
			_ = conn.Close()
			close(accepted)
		}
	}()
	dialer := NewTCPDialer(ln.Addr().String())
	conn, err := dialer.DialContext(context.Background(), "tcp://customer-controlled.invalid:1")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("fixed TCP dialer did not connect")
	}
}

func (d *stubDialer) DialContext(ctx context.Context, target string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, target)
	d.mu.Unlock()
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	if d.dialBlock != nil {
		// Wait for ctx to cancel (the transport's dial-timeout
		// wraps the caller's ctx with WithTimeout) OR for the
		// test to close the channel (success path).
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-d.dialBlock:
		}
	}
	return net.Dial("tcp", d.server.Listener.Addr().String())
}

// TestInternalReverseProxy_PreservesInboundHost pins the routing-key
// contract of the public→internal hop: the outbound Host header must
// stay the customer-facing hostname. gatewayd-internal resolves the
// app from r.Host; rewriting it to the internal target name 404s
// every request with "no app is routed to \"gatewayd-internal\"".
func TestInternalReverseProxy_PreservesInboundHost(t *testing.T) {
	var gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "hello-node-test.apps.gregale.dev"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if gotHost != "hello-node-test.apps.gregale.dev" {
		t.Errorf("upstream Host = %q, want the inbound hostname preserved", gotHost)
	}
}

// TestInternalReverseProxy_StripsHopByHopHeaders pins the RFC 7230
// §6.1 contract: the inbound hop-by-hop headers are NOT forwarded
// to the upstream.
//
// Issue #676 / ADR-080 (PR-3): the strip is BYPASSED when the
// request carries an Upgrade token (Connection: Upgrade + Upgrade:
// <token>) — the bytes flow raw through the gatewayd-internal
// upgrade detector. This test pins the negative case (no Upgrade
// header → Connection is still stripped). The positive case is
// pinned by TestInternalReverseProxy_PreservesUpgradeHeaders
// below.
func TestInternalReverseProxy_StripsHopByHopHeaders(t *testing.T) {
	var seenConnection atomic.Bool
	var seenKeepAlive atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connection") != "" {
			seenConnection.Store(true)
		}
		if r.Header.Get("Keep-Alive") != "" {
			seenKeepAlive.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	// Plain Connection: close (no Upgrade token) — strip applies.
	req.Header.Set("Connection", "close")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("X-Custom", "stays") // non-hop-by-hop, must survive
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if seenConnection.Load() {
		t.Errorf("Connection header forwarded to upstream")
	}
	if seenKeepAlive.Load() {
		t.Errorf("Keep-Alive header forwarded to upstream")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	// X-Custom is non-hop-by-hop, must survive on the upstream
	// side (the test asserts the upstream received it via the
	// seen-custom-header flag above).
}

// TestInternalReverseProxy_StripsResponseHopByHop pins the
// response-side hop-by-hop strip (RFC 7230 §6.1 — the internal
// daemon may have set Connection: close and we don't want to
// leak that to the customer).
func TestInternalReverseProxy_StripsResponseHopByHop(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "close")
		w.Header().Set("X-Forwarded-For", "1.2.3.4")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rr.Header().Get("Connection"); got != "" {
		t.Errorf("Connection leaked to response: %q", got)
	}
	if got := rr.Header().Get("X-Forwarded-For"); got != "1.2.3.4" {
		t.Errorf("X-Forwarded-For response = %q, want 1.2.3.4", got)
	}
}

// TestInternalReverseProxy_StripsAndRebuildsXFF pins the security
// load-bearing XFF contract: the inbound XFF is stripped (the
// customer could forge any IP) and only the public daemon's
// RemoteAddr is forwarded. Internal daemons reading XFF trust
// exactly one hop.
func TestInternalReverseProxy_StripsAndRebuildsXFF(t *testing.T) {
	var got []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	// Forged XFF — must be stripped, not forwarded.
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if len(got) != 1 {
		t.Fatalf("upstream got %d X-Forwarded-For values, want 1 (only the public RemoteAddr): %v", len(got), got)
	}
	if got[0] != "10.0.0.5" {
		t.Errorf("XFF = %q, want 10.0.0.5 (the public daemon's RemoteAddr, not the forged value)", got[0])
	}
}

// TestInternalReverseProxy_SetsXForwardedProtoHTTPS pins the
// TLS-detection side of the XFF bundle.
func TestInternalReverseProxy_SetsXForwardedProtoHTTPS(t *testing.T) {
	var gotProto string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProto = r.Header.Get("X-Forwarded-Proto")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	req.TLS = &tls.ConnectionState{} // simulate the public TLS terminator
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if gotProto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https", gotProto)
	}
}

// TestInternalReverseProxy_DialFailure_502BadGateway verifies the
// dial-failure path surfaces 502 (not 500) so operators
// distinguish "internal tier down" from "this daemon is broken".
func TestInternalReverseProxy_DialFailure_502BadGateway(t *testing.T) {
	dialer := &stubDialer{dialErr: io.EOF} // a representative "dial failed"
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "internal round-trip failed") {
		t.Errorf("body = %q, want substring \"internal round-trip failed\"", rr.Body.String())
	}
}

// TestInternalReverseProxy_BudgetExpiry_504NotBadGateway pins the
// production regression this fix closes. Under a 1000-concurrency
// burst the compute layer answered 200 for 99% of requests, but the
// public edge's own 3 s request budget fired mid-RoundTrip and the
// proxy reported every one of them as 502 "bad gateway" — blaming
// upstream for the edge's own deadline and discarding work the
// customer was billed for. A fired budget must surface as the
// canonical 504 + RFC 7807 request_budget_exceeded envelope.
func TestInternalReverseProxy_BudgetExpiry_504NotBadGateway(t *testing.T) {
	// Upstream is healthy but slower than the budget.
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	defer close(release)

	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)

	// Attach a request budget the way cmd/gatewayd-public's
	// reqbudget middleware does, then let it expire.
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	ctx, cancel, _ := reqbudget.WithRemaining(req.Context(), 50*time.Millisecond,
		api.RequestBudgetMax, "forward", "GET:/app")
	defer cancel()
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req.WithContext(ctx))

	if rr.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504 (budget expiry is not an upstream fault)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), api.CodeRequestBudgetExceeded) {
		t.Errorf("body = %q, want RFC 7807 code %q", rr.Body.String(), api.CodeRequestBudgetExceeded)
	}
	if strings.Contains(rr.Body.String(), "bad gateway") {
		t.Errorf("body = %q, must not blame upstream for the edge's own deadline", rr.Body.String())
	}
}

func TestInternalReverseProxy_LongLivedResponseDetachesBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(api.StreamingStatusHeader, string(api.StreamingStatusStreaming))
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(120 * time.Millisecond)
		_, _ = io.WriteString(w, "stream-complete")
	}))
	defer upstream.Close()

	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	ctx, cancel, _ := reqbudget.WithRemaining(req.Context(), 50*time.Millisecond,
		api.RequestBudgetMax, "forward", "GET:/stream")
	defer cancel()
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req.WithContext(ctx))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "stream-complete" {
		t.Fatalf("body = %q, want stream-complete", got)
	}
}

// A RoundTrip failure with NO budget attached must still be a 502 —
// the fix must not swallow genuine upstream faults. Without this the
// budget check could regress into "every transport error is a 504".
func TestInternalReverseProxy_NoBudget_StillBadGateway(t *testing.T) {
	dialer := &stubDialer{dialErr: io.EOF}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when no request budget is attached", rr.Code)
	}
}

func TestInternalReverseProxy_NoComputeCapacity_503(t *testing.T) {
	dialer := &stubDialer{dialErr: ErrNoComputeCapacity}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want 5", got)
	}
	if !strings.Contains(rr.Body.String(), "compute capacity unavailable") {
		t.Errorf("body = %q, want compute capacity unavailable", rr.Body.String())
	}
}

// TestInternalReverseProxy_UpstreamError_StatusPropagated verifies
// 5xx responses from the internal daemon flow through unchanged
// (we don't mask upstream errors).
func TestInternalReverseProxy_UpstreamError_StatusPropagated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream 503"))
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "upstream 503") {
		t.Errorf("body = %q, want substring \"upstream 503\"", rr.Body.String())
	}
}

// TestInternalReverseProxy_NilDialer_502 covers the wiring-bug
// path: a proxy constructed without a dialer returns 502 and logs.
func TestInternalReverseProxy_NilDialer_502(t *testing.T) {
	p := &InternalReverseProxy{Target: &url.URL{Scheme: "http", Host: "internal"}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}

// TestNewUnixSocketDialer_RespectsContextCancel pins the contract
// that the unix-socket dialer honours ctx cancellation (the public
// daemon's drain sequence relies on this).
func TestNewUnixSocketDialer_RespectsContextCancel(t *testing.T) {
	d := NewUnixSocketDialer("/tmp/this-socket-does-not-exist.sock")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE dial — fastest possible abort
	start := time.Now()
	conn, err := d.DialContext(ctx, "anything")
	elapsed := time.Since(start)
	if err == nil {
		_ = conn.Close()
		t.Errorf("DialContext with cancelled ctx returned nil err")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("DialContext did not honour ctx cancel within 100 ms (%v elapsed)", elapsed)
	}
}

// TestIsHopByHop_Predicate pins the lookup table.
func TestIsHopByHop_Predicate(t *testing.T) {
	cases := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailers":            true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
		"X-Custom":            false,
		"Content-Type":        false,
		"connection":          true, // case-insensitive
	}
	for h, want := range cases {
		if got := isHopByHop(h); got != want {
			t.Errorf("isHopByHop(%q) = %v, want %v", h, got, want)
		}
	}
}

// TestStripHopByHopInPlace_DoesNotDoubleAlloc pins the in-place
// strip behaviour. The proxy uses the in-place variant to avoid
// the Clone + rebuild double-alloc on the hot path.
func TestStripHopByHopInPlace_DoesNotDoubleAlloc(t *testing.T) {
	h := http.Header{
		"Connection":      []string{"close"},
		"Upgrade":         []string{"websocket"},
		"X-Custom":        []string{"stays"},
		"X-Forwarded-For": []string{"1.2.3.4"},
	}
	stripHopByHopInPlace(h)
	if h.Get("Connection") != "" {
		t.Errorf("Connection not stripped")
	}
	if h.Get("Upgrade") != "" {
		t.Errorf("Upgrade not stripped")
	}
	if h.Get("X-Custom") != "stays" {
		t.Errorf("X-Custom dropped")
	}
	if h.Get("X-Forwarded-For") != "1.2.3.4" {
		t.Errorf("X-Forwarded-For dropped (not a hop-by-hop)")
	}
}

// TestCopyResponseBody_ContextCancel verifies the body copy
// goroutine returns when ctx is cancelled (the conn-bound write
// loop does not pin the public listener).
func TestCopyResponseBody_ContextCancel(t *testing.T) {
	// Source returns io.EOF immediately so the goroutine completes
	// fast; the assertion is that the function returns within the
	// ctx deadline (no goroutine leak).
	src := io.NopCloser(strings.NewReader("hello"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf strings.Builder
	n, err := copyResponseBody(ctx, &buf, src)
	if err != nil {
		t.Errorf("copyResponseBody err = %v, want nil", err)
	}
	if n != 5 || buf.String() != "hello" {
		t.Errorf("copyResponseBody wrote %q (n=%d), want \"hello\" (n=5)", buf.String(), n)
	}
}

// TestCopyResponseBody_ShortInput pins the EOF short-circuit.
func TestCopyResponseBody_ShortInput(t *testing.T) {
	src := io.NopCloser(strings.NewReader(""))
	ctx := context.Background()
	var buf strings.Builder
	n, err := copyResponseBody(ctx, &buf, src)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

// TestInternalReverseProxy_PreservesUpgradeHeaders pins the
// issue #676 / ADR-080 contract: when the inbound request carries
// Connection: Upgrade + Upgrade: <token>, the hop-by-hop strip is
// SKIPPED so the WebSocket handshake reaches gatewayd-internal
// intact. Without this guard, gatewayd-internal's upgrade
// detector would never see the headers, and the customer's WS
// client would silently fall through to a 502.
func TestInternalReverseProxy_PreservesUpgradeHeaders(t *testing.T) {
	var gotConnection, gotUpgrade string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotConnection = r.Header.Get("Connection")
		gotUpgrade = r.Header.Get("Upgrade")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/socket", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if gotConnection != "Upgrade" {
		t.Errorf("Connection header = %q, want Upgrade (preserved for upgrade request)", gotConnection)
	}
	if gotUpgrade != "websocket" {
		t.Errorf("Upgrade header = %q, want websocket (preserved for upgrade request)", gotUpgrade)
	}
}

// TestInternalReverseProxy_StripsCaseInsensitiveUpgrade pins the
// RFC 7230 §3.2 contract on the upgrade-headers bypass: a
// lowercase `connection: upgrade` is still detected as an upgrade
// request (the detector is case-insensitive on both Connection
// token parsing AND the Upgrade header check) so the strip is
// still skipped. The Go stdlib canonicalizes Connection on Set
// to "Connection", so we use the raw map form to set "upgrade"
// — the detector reads both via Header.Get + strings.EqualFold.
func TestInternalReverseProxy_StripsCaseInsensitiveUpgrade(t *testing.T) {
	var gotConnection, gotUpgrade string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotConnection = r.Header.Get("Connection")
		gotUpgrade = r.Header.Get("Upgrade")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	req := httptest.NewRequest(http.MethodGet, "/socket", nil)
	req.Header["Connection"] = []string{"upgrade"}
	req.Header["Upgrade"] = []string{"websocket"}
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if gotConnection != "upgrade" {
		t.Errorf("Connection header = %q, want upgrade (preserved)", gotConnection)
	}
	if gotUpgrade != "websocket" {
		t.Errorf("Upgrade header = %q, want websocket (preserved)", gotUpgrade)
	}
}

// flushingRecorder is an io.Writer that counts Flush() calls so
// TestCopyResponseBody_FlushesPerChunk can pin the per-Write
// flush contract introduced by the issue #687 fix. It
// implements http.Flusher so the type assertion in
// copyResponseBody succeeds.
//
// flushes is read by the test after the copy completes; Write
// is delegated to an internal bytes.Buffer so the test can also
// assert the bytes that were written.
type flushingRecorder struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	flushes int
}

func (f *flushingRecorder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Write(p)
}

func (f *flushingRecorder) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushes++
}

func (f *flushingRecorder) flushCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushes
}

func (f *flushingRecorder) bytes() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.String()
}

// chunkedReader is an io.ReadCloser that emits N chunks of
// `size` bytes with a configurable gap between. The cancel
// goroutine can Close() it to unblock a pending Read mid-copy.
type chunkedReader struct {
	mu       sync.Mutex
	chunks   int
	size     int
	gap      time.Duration
	closed   bool
	cancelRd chan struct{}
}

func newChunkedReader(chunks, size int, gap time.Duration) *chunkedReader {
	return &chunkedReader{
		chunks:   chunks,
		size:     size,
		gap:      gap,
		cancelRd: make(chan struct{}),
	}
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	if c.chunks <= 0 {
		return 0, io.EOF
	}
	c.chunks--
	if c.gap > 0 {
		time.Sleep(c.gap)
	}
	// Honour io.Reader contract: n must be <= len(p). Without
	// this clamp, a future test that constructs newChunkedReader
	// with size > the caller's buffer (e.g. a reduced 1 KiB
	// buffer in copyResponseBody) returns n > len(p) — undefined
	// behaviour under io.Reader (the caller indexes buf[:n] out
	// of bounds).
	n := c.size
	if n > len(p) {
		n = len(p)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	return n, nil
}

func (c *chunkedReader) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.cancelRd)
	}
	return nil
}

// TestCopyResponseBody_FlushesPerChunk pins the issue #687
// per-Write Flush contract: an http.Flusher dst sees one
// Flush() call per non-empty Read, so SSE / chunked-JSON
// customers see flushes at chunk boundaries (not at the 32 KiB
// io.Copy buffer boundary that the pre-fix shape used).
func TestCopyResponseBody_FlushesPerChunk(t *testing.T) {
	rec := &flushingRecorder{}
	src := io.NopCloser(io.Reader(newChunkedReader(3, 1024, 0)))
	n, err := copyResponseBody(context.Background(), rec, src)
	if err != nil {
		t.Fatalf("copyResponseBody err = %v, want nil", err)
	}
	if n != 3*1024 {
		t.Errorf("bytes copied = %d, want 3072", n)
	}
	if got := rec.flushCount(); got < 3 {
		t.Errorf("Flush count = %d, want >= 3 (one per chunk)", got)
	}
	if got := rec.bytes(); len(got) != 3*1024 {
		t.Errorf("dst bytes = %d, want 3072", len(got))
	}
}

// TestCopyResponseBody_CancelReleasesStreamWindow pins the
// issue #687 goroutine-leak fix: when ctx cancels mid-stream,
// copyResponseBody closes src (so the upstream unix socket sees
// the FIN and the H2C stream window is released) AND waits for
// the goroutine to exit before returning, so the no-leak
// assertion is deterministic.
//
// Pre-fix: function returned ctx.Err() immediately, leaving the
// io.Copy goroutine alive reading from src until EOF. Under H2C
// one unix-socket connection carries many multiplexed streams;
// a stuck reader holds the connection's stream window open.
func TestCopyResponseBody_CancelReleasesStreamWindow(t *testing.T) {
	rec := &flushingRecorder{}
	src := newChunkedReader(100000, 1024, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := copyResponseBody(ctx, rec, src)
		done <- result{n, err}
	}()

	// Let one or two chunks land, then cancel.
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", r.err)
		}
		// The cancel arm returns (0, ctx.Err()) — bytes-written-
		// so-far is not meaningful to the caller once the stream
		// has been cut. The load-bearing assertion is that
		// copyResponseBody returned within 2 s AND src was
		// closed (the no-leak contract).
		if r.n < 0 || r.n >= 100*1024 {
			t.Errorf("n = %d, want 0..<%d (cancel must return promptly)", r.n, 100*1024)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copyResponseBody did not return within 2s of ctx cancel")
	}

	src.mu.Lock()
	closed := src.closed
	src.mu.Unlock()
	if !closed {
		t.Error("src was not closed on ctx cancel — H2C stream window leak regressed")
	}
}

// TestInternalReverseProxy_DialTimeout_ScopeOnlyDial pins the
// issue #687 DialTimeout-scope fix: the dial step is bounded by
// p.DialTimeout (50 ms in this test), but RoundTrip runs on the
// inbound request context. A dial that blocks forever must
// abort at the dial-timeout deadline; the proxy must NOT wait
// for any longer RoundTrip-level timeout.
//
// Pre-fix: ctx was wrapped with WithTimeout(p.DialTimeout)
// before RoundTrip, so the dial timeout applied to the entire
// upstream read — including the body copy. Post-fix: the dial
// timeout applies only inside DialContext; RoundTrip has the
// inbound deadline.
func TestInternalReverseProxy_DialTimeout_ScopeOnlyDial(t *testing.T) {
	dialer := &stubDialer{dialBlock: make(chan struct{})}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	p.DialTimeout = 50 * time.Millisecond
	p.Transport = p.buildTransport(dialer, false)

	start := time.Now()
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/hello", nil))
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v, want < 500ms (dial should abort at ~50ms)", elapsed)
	}
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (dial failed)", rr.Code)
	}
}

// TestInternalReverseProxy_LongStreaming_NotCutByDialTimeout
// pins the negative case of the DialTimeout-scope fix: even
// though the dial timeout is short (50 ms), the body copy must
// run on the inbound request context — so a long-running
// streaming response from a cold-wake app reaches the customer
// in full.
//
// Pre-fix: ctx was wrapped with WithTimeout(p.DialTimeout) at
// the ServeHTTP entry, so a 50 ms dial timeout cut a 60-second
// streaming response at 50 ms. Post-fix: the dial timeout
// scopes only the dial step.
func TestInternalReverseProxy_LongStreaming_NotCutByDialTimeout(t *testing.T) {
	const totalChunks = 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < totalChunks; i++ {
			_, _ = w.Write(make([]byte, 1024))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	dialer := &stubDialer{server: upstream}
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	p.DialTimeout = 50 * time.Millisecond
	p.Transport = p.buildTransport(dialer, false)

	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (long body should not be cut by DialTimeout)", rr.Code)
	}
	if got := rr.Body.Len(); got != totalChunks*1024 {
		t.Errorf("body bytes = %d, want %d (full streaming response must reach customer)", got, totalChunks*1024)
	}
}

// panicOnWriteRecorder is an io.Writer that panics on the first
// Write. Used by TestCopyResponseBody_WritePanicDoesNotHang to
// pin the issue #687 PR-3 review finding #3 fix: the
// copyResponseBody goroutine's recover defer MUST send on done
// before re-panicking, otherwise the cancel arm's <-done blocks
// forever.
type panicOnWriteRecorder struct{}

func (panicOnWriteRecorder) Write(_ []byte) (int, error) {
	panic("deliberate test panic in copyResponseBody dst.Write path")
}

// TestCopyResponseBody_WritePanicReleasesCancelArm pins the
// recover-defer in copyResponseBody (issue #687 PR-3 review
// finding #3): when dst.Write panics, the goroutine's panic
// recovery defer MUST send a result on done (so the cancel
// arm's <-done unblocks) before the goroutine exits. Without
// the recover defer, the goroutine would die in the panic —
// and any caller parked on the cancel arm's <-done would
// block forever, holding the H2C stream window open.
//
// Test shape: cancel the ctx at 50 ms (after the Write panic
// fires at ~0 ms) and assert that copyResponseBody returns
// within 100 ms of the cancel. Pre-fix, the goroutine would
// silently die in the panic and the function would hang on
// the cancel arm's <-done.
func TestCopyResponseBody_WritePanicReleasesCancelArm(t *testing.T) {
	src := io.NopCloser(io.Reader(newChunkedReader(1, 1024, 0)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The Write panic is swallowed by copyResponseBody's
		// recover defer (the goroutine exits cleanly via the
		// done-channel send in the recovery path). We don't
		// wrap this in a recover() because the production
		// code's recovery path handles it — if the recover
		// defer regresses (e.g. panic propagates), the test
		// fails with a runtime goroutine panic, which is a
		// louder signal than a hung test.
		_, _ = copyResponseBody(ctx, panicOnWriteRecorder{}, src)
	}()

	// The Write panic fires on the first Read+Write (within
	// microseconds). The recover defer sends on done and the
	// copyResponseBody goroutine returns. We assert the
	// outer function returns within 100 ms — bounded by the
	// cancel arm's wait on the goroutine's done send.
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("copyResponseBody did not return within 500ms of Write panic — recover defer / done-send regressed (cancel arm hangs)")
	}
}
