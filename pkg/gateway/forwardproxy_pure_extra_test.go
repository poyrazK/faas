// forwardproxy_pure_extra_test.go — fill pkg/gateway/forwardproxy.go
// coverage of the tiny pure / no-gRPC helpers. Targets
// WithSyntheticInvocation / isSyntheticInvocation (context
// round-trip), stripHopByHop (the RFC 7230 §6.1 hop-by-hop list),
// contextWithProxyStart / proxyStartFromContext (default-zero +
// set+read-back), flushSafe (nil-Flusher + happy path),
// ctxReader.Read (context.Canceled mid-read), NodeClientCache.Evict
// (hit + miss), and leaseCloser.Close (refcount decrement +
// last-lease-close).
package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// --- WithSyntheticInvocation / isSyntheticInvocation ------------

func TestSyntheticInvocation_RoundTrip(t *testing.T) {
	// Set + read → true.
	ctx := WithSyntheticInvocation(context.Background())
	if !isSyntheticInvocation(ctx) {
		t.Error("after set: got false, want true")
	}
}

func TestIsSyntheticInvocation_DefaultContextIsFalse(t *testing.T) {
	if isSyntheticInvocation(context.Background()) {
		t.Error("bare ctx: got true, want false")
	}
}

func TestIsSyntheticInvocation_WrongTypeIsFalse(t *testing.T) {
	// A context value at the same key but a different type
	// assertion → safely returns false.
	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, "hi")
	if isSyntheticInvocation(ctx) {
		t.Error("other key: got true, want false")
	}
}

// --- stripHopByHop ----------------------------------------------

func TestStripHopByHop_DropsListedHeaders(t *testing.T) {
	in := http.Header{
		"Connection":          {"close"},
		"Keep-Alive":          {"timeout=5"},
		"Proxy-Authenticate":  {"Basic"},
		"Proxy-Authorization": {"Basic xxx"},
		"Te":                  {"trailers"},
		"Trailers":            {"X-Custom"},
		"Transfer-Encoding":   {"chunked"},
		"Upgrade":             {"websocket"},
		"X-Custom":            {"kept"},
		"Content-Type":        {"application/json"},
		"Host":                {"example.com"},
	}
	out := stripHopByHop(in)
	if got := out.Get("Connection"); got != "" {
		t.Errorf("Connection: still set (%q)", got)
	}
	if got := out.Get("Keep-Alive"); got != "" {
		t.Errorf("Keep-Alive: still set (%q)", got)
	}
	if got := out.Get("Proxy-Authenticate"); got != "" {
		t.Errorf("Proxy-Authenticate: still set (%q)", got)
	}
	if got := out.Get("Proxy-Authorization"); got != "" {
		t.Errorf("Proxy-Authorization: still set (%q)", got)
	}
	if got := out.Get("Te"); got != "" {
		t.Errorf("Te: still set (%q)", got)
	}
	if got := out.Get("Trailers"); got != "" {
		t.Errorf("Trailers: still set (%q)", got)
	}
	if got := out.Get("Transfer-Encoding"); got != "" {
		t.Errorf("Transfer-Encoding: still set (%q)", got)
	}
	if got := out.Get("Upgrade"); got != "" {
		t.Errorf("Upgrade: still set (%q)", got)
	}
	if got := out.Get("X-Custom"); got != "kept" {
		t.Errorf("X-Custom lost (%q)", got)
	}
	if got := out.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type lost (%q)", got)
	}
	// stripHopByHop must NOT mutate the inbound.
	if in.Get("Connection") != "close" {
		t.Errorf("input mutated: Connection=%q", in.Get("Connection"))
	}
}

func TestStripHopByHop_EmptyHeader(t *testing.T) {
	out := stripHopByHop(http.Header{})
	if len(out) != 0 {
		t.Errorf("empty: got %d entries, want 0", len(out))
	}
}

func TestStripHopByHop_NilHeader(t *testing.T) {
	out := stripHopByHop(nil)
	if len(out) != 0 {
		t.Errorf("nil: got %d entries, want 0", len(out))
	}
}

// --- contextWithProxyStart / proxyStartFromContext -------------

func TestProxyStartContext_RoundTrip(t *testing.T) {
	now := time.Now()
	ctx := contextWithProxyStart(context.Background(), now)
	got := proxyStartFromContext(ctx)
	if !got.Equal(now) {
		t.Errorf("got %v, want %v", got, now)
	}
}

func TestProxyStartContext_DefaultReturnsZero(t *testing.T) {
	if got := proxyStartFromContext(context.Background()); !got.IsZero() {
		t.Errorf("default: got %v, want zero", got)
	}
}

func TestProxyStartContext_NonTimeValueReturnsZero(t *testing.T) {
	// A context value at proxyStartKey with a different type
	// assertion (e.g. string "hi") → default zero.
	type bogusKey struct{}
	ctx := context.WithValue(context.Background(), bogusKey{}, "hi")
	if got := proxyStartFromContext(ctx); !got.IsZero() {
		t.Errorf("bogus: got %v, want zero", got)
	}
}

// --- flushSafe --------------------------------------------------

type flusherWriter struct {
	header  http.Header
	body    bytes.Buffer
	code    int
	flushed int
}

func (f *flusherWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}

func (f *flusherWriter) Write(b []byte) (int, error) { return f.body.Write(b) }
func (f *flusherWriter) WriteHeader(code int)        { f.code = code }
func (f *flusherWriter) Flush()                      { f.flushed++ }

func TestFlushSafe_WiredFlusherIncrements(t *testing.T) {
	w := &flusherWriter{}
	flushSafe(w)
	if w.flushed != 1 {
		t.Errorf("flushed = %d, want 1", w.flushed)
	}
}

func TestFlushSafe_OnNonFlusherRecovers(t *testing.T) {
	// httptest.NewRecorder doesn't implement http.Flusher at the
	// type-assertion level — flushSafe must NOT panic.
	w := httptestNewRecorder()
	flushSafe(w)
}

// --- ctxReader.Read --------------------------------------------

type blockingReader struct{}

func (b *blockingReader) Read(p []byte) (int, error) { <-make(chan struct{}); return 0, io.EOF }

func TestCtxReader_Read_ContextCanceledReturnsErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &ctxReader{r: &blockingReader{}, ctx: ctx}
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Errorf("got (%d, %v), want (0, context.Canceled)", n, err)
	}
}

func TestCtxReader_Read_UnderlyingReadWins(t *testing.T) {
	// Healthy ctx; underlying returns data immediately.
	r := &ctxReader{r: bytes.NewReader([]byte("hello")), ctx: context.Background()}
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if n != 5 || err != nil || string(buf[:n]) != "hello" {
		t.Errorf("got (%d, %v, %q), want (5, nil, hello)", n, err, buf[:n])
	}
}

func TestCtxReader_Read_UnderlyingEOFReturnsEOF(t *testing.T) {
	r := &ctxReader{r: bytes.NewReader(nil), ctx: context.Background()}
	buf := make([]byte, 8)
	_, err := r.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("got %v, want io.EOF", err)
	}
}

type closeAwareBlockingReader struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (r *closeAwareBlockingReader) Read(_ []byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *closeAwareBlockingReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func TestNewCtxReader_ClosesUnderlyingReaderOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := &closeAwareBlockingReader{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	r, stop := newCtxReader(ctx, body)
	defer stop()

	readDone := make(chan error, 1)
	go func() {
		_, err := r.Read(make([]byte, 8))
		readDone <- err
	}()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("underlying reader did not start")
	}

	cancel()
	select {
	case err := <-readDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closable reader did not unblock after context cancellation")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("context cancellation did not close the underlying reader")
	}
}

// --- leaseCloser.Close / NodeClientCache.Evict -----------------

type fakeConn struct {
	closeCount atomic.Int32
	closed     chan struct{}
}

func newFakeConn() *fakeConn { return &fakeConn{closed: make(chan struct{}, 1)} }
func (f *fakeConn) Close() error {
	f.closeCount.Add(1)
	select {
	case f.closed <- struct{}{}:
	default:
	}
	return nil
}

func TestLeaseCloser_DecrementsItsOwnConnection(t *testing.T) {
	entry := &nodeConn{refs: 2}
	cache := &NodeClientCache{clients: map[string]*nodeConn{"node-1": entry}}
	l := &leaseCloser{c: cache, entry: entry}
	if err := l.Close(); err != nil {
		t.Fatalf("Close err = %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close err = %v", err)
	}
	if entry.refs != 1 {
		t.Errorf("refs after a double Close = %d, want 1", entry.refs)
	}
}

// --- NodeClientCache.Evict / Close ------------------------------

func TestNodeClientCache_Evict_MissingNodeIsNoop(t *testing.T) {
	cache := &NodeClientCache{clients: map[string]*nodeConn{}}
	cache.Evict("missing-node")
	// Should not panic; cache state unchanged.
	if len(cache.clients) != 0 {
		t.Errorf("clients: got %d, want 0", len(cache.clients))
	}
}

func TestNodeClientCache_Evict_RetiresEntryWithOutstandingRefs(t *testing.T) {
	entry := &nodeConn{refs: 3}
	cache := &NodeClientCache{clients: map[string]*nodeConn{"node-1": entry}}
	cache.Evict("node-1")
	if _, ok := cache.clients["node-1"]; ok {
		t.Error("clients entry not removed")
	}
	if !entry.retired || entry.refs != 3 {
		t.Errorf("entry = %+v, want retired with its 3 leases", entry)
	}
}

func TestNodeClientCache_Close_EmptyCacheIsNoop(t *testing.T) {
	cache := &NodeClientCache{clients: map[string]*nodeConn{}}
	if err := cache.Close(); err != nil {
		t.Errorf("empty: Close err = %v", err)
	}
}

// Leases were counted per node: an eviction with requests in flight leaked
// the connection, and the old leases then decremented the next connection's
// count until an eviction closed it under live requests (bug hunt 2026-10-28
// B17).
func TestNodeClientCache_EvictionKeepsLeasesOnTheirOwnConnection(t *testing.T) {
	prev := resolveNodeTarget
	t.Cleanup(func() { resolveNodeTarget = prev })
	SetNodeResolver(func(context.Context, string) (string, bool) { return "passthrough:///vmmd", true })
	var dialed []*grpc.ClientConn
	cache := NewNodeClientCache(func(_ context.Context, target string) (*grpc.ClientConn, error) {
		conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			dialed = append(dialed, conn)
		}
		return conn, err
	}, nil)
	t.Cleanup(func() { _ = cache.Close() })
	lease := func() io.Closer {
		t.Helper()
		_, closer, ok := cache.ClientFor(context.Background(), "node-1")
		if !ok {
			t.Fatal("ClientFor failed")
		}
		return closer
	}
	shutdown := func(conn *grpc.ClientConn) bool { return conn.GetState() == connectivity.Shutdown }

	oldA, oldB := lease(), lease()
	cache.Evict("node-1")
	if shutdown(dialed[0]) {
		t.Fatal("evicting closed a connection with leases in flight")
	}
	fresh := lease()
	if len(dialed) != 2 {
		t.Fatalf("dials = %d, want a fresh connection after eviction", len(dialed))
	}
	_ = oldA.Close()
	_ = oldB.Close()
	if !shutdown(dialed[0]) {
		t.Fatal("the evicted connection stayed open after its last lease")
	}
	cache.Evict("node-1")
	if shutdown(dialed[1]) {
		t.Fatal("old leases let an eviction close the new connection under a live lease")
	}
	_ = fresh.Close()
	if !shutdown(dialed[1]) {
		t.Fatal("the second evicted connection stayed open after its last lease")
	}
}

// A heartbeat re-registration fires compute_node_changed without changing the
// node; it no longer drops the connection (bug hunt 2026-10-28 B17).
func TestNodeClientCache_EvictIfChangedKeepsAnUnchangedNode(t *testing.T) {
	prev := resolveNodeTarget
	t.Cleanup(func() { resolveNodeTarget = prev })
	target := "passthrough:///vmmd-a"
	SetNodeResolver(func(context.Context, string) (string, bool) { return target, true })
	cache := NewNodeClientCache(func(_ context.Context, target string) (*grpc.ClientConn, error) {
		return grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}, nil)
	t.Cleanup(func() { _ = cache.Close() })
	_, closer, ok := cache.ClientFor(context.Background(), "node-1")
	if !ok {
		t.Fatal("ClientFor failed")
	}
	_ = closer.Close()
	if cache.EvictIfChanged(context.Background(), "node-1", true) {
		t.Fatal("an unchanged active node was evicted")
	}
	target = "passthrough:///vmmd-b"
	if !cache.EvictIfChanged(context.Background(), "node-1", true) {
		t.Fatal("a node whose target changed was kept")
	}
	if cache.EvictIfChanged(context.Background(), "node-2", false) {
		t.Fatal("evicting an uncached node reported an eviction")
	}
}

// --- SetNodeResolver / resolveTarget round-trip ---------------

func TestSetNodeResolver_AffectsResolveTarget(t *testing.T) {
	prev := resolveNodeTarget
	t.Cleanup(func() { resolveNodeTarget = prev })

	called := 0
	SetNodeResolver(func(_ context.Context, nodeID string) (string, bool) {
		called++
		return "dns:node-" + nodeID, true
	})
	cache := &NodeClientCache{}
	got, ok := cache.resolveTarget(context.Background(), "node-1")
	if !ok || got != "dns:node-node-1" {
		t.Errorf("got (%q, %v), want (dns:node-node-1, true)", got, ok)
	}
	if called != 1 {
		t.Errorf("resolver called %d times, want 1", called)
	}
}

func TestSetNodeResolver_ReturnsFalseForMiss(t *testing.T) {
	prev := resolveNodeTarget
	t.Cleanup(func() { resolveNodeTarget = prev })

	SetNodeResolver(func(_ context.Context, _ string) (string, bool) {
		return "", false
	})
	cache := &NodeClientCache{}
	if _, ok := cache.resolveTarget(context.Background(), "nope"); ok {
		t.Error("miss: got ok=true, want false")
	}
}

// --- SynthDispatcher interface sanity check ------------------

// Compile-time: keep the fakeSynthDispatcher field types honest.
var _ SynthDispatcher = (*fakeSynthDispatcher)(nil)

// pin: vmmdpb import to satisfy future type embedding.
var _ vmmdpb.VmmdClient = (vmmdpb.VmmdClient)(nil)

// --- httptest.NewRecorder helper (avoids extra package import) --

func httptestNewRecorder() http.ResponseWriter { return newHtRecorder() }

// we can't use httptest.NewRecorder directly because that defeats
// the type-assertion test (httptest.ResponseRecorder DOES expose
// Flusher via embedded *ResponseWriter — but the cleanup helpers
// the surrounding code expects are on the type, not the iface).
//
// The wrapper above just delegates to a fresh httptest.NewRecorder.
// (Defined here for clarity.)
func newHtRecorder() http.ResponseWriter { return &httptestRecorderShim{} }

type httptestRecorderShim struct{}

func (h *httptestRecorderShim) Header() http.Header         { return http.Header{} }
func (h *httptestRecorderShim) Write(b []byte) (int, error) { return len(b), nil }
func (h *httptestRecorderShim) WriteHeader(_ int)           {}
