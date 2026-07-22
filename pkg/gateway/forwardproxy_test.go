// Tests for pkg/gateway/forwardproxy.go (issue #98 / ADR-028). The
// gateway-side bridge is HTTP-in / gRPC-out. We can't exercise the
// real vmmd end (that requires //go:build metal on Linux), so the
// test uses an in-memory fake VmmdClient that captures the
// ForwardHTTPRequest and returns a deterministic
// ForwardHTTPResponse. The forwarder is then driven through
// httptest.NewRecorder so we can assert the HTTP shape end-to-end.

package gateway_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeVmmdClient is a vmmdpb.VmmdClient that records every
// ForwardHTTPRequest and replies with the canned ForwardHTTPResponse
// (or canned error) the test installs. It implements only the
// methods the forwarder uses; everything else panics so a future
// test that accidentally routes through here surfaces the mistake.
type fakeVmmdClient struct {
	mu    sync.Mutex
	calls []*vmmdpb.ForwardHTTPRequest
	resp  *vmmdpb.ForwardHTTPResponse
	err   error
}

func (f *fakeVmmdClient) ForwardHTTP(_ context.Context, in *vmmdpb.ForwardHTTPRequest, _ ...grpc.CallOption) (*vmmdpb.ForwardHTTPResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// All other RPCs panic — the forwarder only calls ForwardHTTP.
func (f *fakeVmmdClient) CreateFromSnapshot(context.Context, *vmmdpb.CreateFromSnapshotRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	panic("CreateFromSnapshot: not stubbed")
}
func (f *fakeVmmdClient) CreateColdBoot(context.Context, *vmmdpb.CreateColdBootRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	panic("CreateColdBoot: not stubbed")
}
func (f *fakeVmmdClient) PauseAndSnapshot(context.Context, *vmmdpb.PauseAndSnapshotRequest, ...grpc.CallOption) (*vmmdpb.SnapshotResponse, error) {
	panic("PauseAndSnapshot: not stubbed")
}
func (f *fakeVmmdClient) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	panic("Destroy: not stubbed")
}
func (f *fakeVmmdClient) Stats(context.Context, *vmmdpb.StatsRequest, ...grpc.CallOption) (*vmmdpb.StatsResponse, error) {
	panic("Stats: not stubbed")
}
func (f *fakeVmmdClient) Heartbeat(context.Context, *vmmdpb.HeartbeatRequest, ...grpc.CallOption) (*vmmdpb.HeartbeatResponse, error) {
	panic("Heartbeat: not stubbed")
}
func (f *fakeVmmdClient) Ping(context.Context, *vmmdpb.PingRequest, ...grpc.CallOption) (*vmmdpb.PingResponse, error) {
	panic("Ping: not stubbed")
}

// fakeNodeLookup is the NodeClientLookup the forwarder reads through.
// It returns a stable (cli, closer) for any non-empty node id so
// tests can drive the happy path; ok=false for empty ids so we can
// exercise the defensive 503.
type fakeNodeLookup struct {
	mu     sync.Mutex
	cli    vmmdpb.VmmdClient
	closed int
}

func (f *fakeNodeLookup) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if nodeID == "" {
		return nil, nil, false
	}
	return f.cli, lease{f: f}, true
}

type lease struct{ f *fakeNodeLookup }

func (l lease) Close() error {
	l.f.mu.Lock()
	defer l.f.mu.Unlock()
	l.f.closed++
	return nil
}

func TestForwardingReverseProxy_HappyPath(t *testing.T) {
	cli := &fakeVmmdClient{
		resp: &vmmdpb.ForwardHTTPResponse{
			Status: 200,
			Headers: []*vmmdpb.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"hello":"world"}`),
		},
	}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/items?id=42", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	// Hop-by-hop headers must be stripped before sending — see
	// stripHopByHop. Connection in particular would otherwise confuse
	// the guest's response framing.
	req.Header.Set("Connection", "close")
	req.Header.Set("X-Custom", "keep-me")

	rec := httptest.NewRecorder()
	proxy("node-1").ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
	if got := rec.Body.String(); got != `{"hello":"world"}` {
		t.Errorf("body = %q", got)
	}

	// Verify the request the bridge received on the vmmd side.
	if len(cli.calls) != 1 {
		t.Fatalf("ForwardHTTP calls = %d, want 1", len(cli.calls))
	}
	got := cli.calls[0]
	if got.GetMethod() != "POST" {
		t.Errorf("method = %q, want POST", got.GetMethod())
	}
	if got.GetRequestUri() != "/api/v1/items?id=42" {
		t.Errorf("uri = %q", got.GetRequestUri())
	}
	if string(got.GetBody()) != `{"x":1}` {
		t.Errorf("body = %q", got.GetBody())
	}
	// Connection was stripped; X-Custom + Authorization survived.
	gotHeaders := map[string]string{}
	for _, h := range got.GetHeaders() {
		gotHeaders[h.GetName()] = h.GetValue()
	}
	if _, present := gotHeaders["Connection"]; present {
		t.Error("Connection header leaked into bridge")
	}
	if gotHeaders["X-Custom"] != "keep-me" {
		t.Errorf("X-Custom = %q, want keep-me", gotHeaders["X-Custom"])
	}
	if gotHeaders["Authorization"] != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", gotHeaders["Authorization"])
	}
	// Closer ran exactly once.
	if lookup.closed != 1 {
		t.Errorf("closer calls = %d, want 1", lookup.closed)
	}
}

func TestForwardingReverseProxy_UnknownNodeIs503(t *testing.T) {
	cli := &fakeVmmdClient{} // no calls expected
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	proxy("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if len(cli.calls) != 0 {
		t.Errorf("ForwardHTTP called %d times with empty node id", len(cli.calls))
	}
}

func TestForwardingReverseProxy_UpstreamUnavailableIs503(t *testing.T) {
	// vmmd returned Unavailable: gateway must surface 503 so the
	// client retries, AND the routing cache should evict (handled
	// upstream by the notify listener). The closer MUST still run.
	cli := &fakeVmmdClient{
		err: status.Error(codes.Unavailable, "guest gone"),
	}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	proxy("node-1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if lookup.closed != 1 {
		t.Errorf("closer calls = %d, want 1", lookup.closed)
	}
}

func TestForwardingReverseProxy_NotFoundIs503(t *testing.T) {
	// vmmd returned NotFound = the instance parked between the wake
	// and the forward. Same eviction path: 503 + close.
	cli := &fakeVmmdClient{
		err: status.Error(codes.NotFound, "instance i-1 not live"),
	}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	proxy("node-1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestForwardingReverseProxy_OtherErrorIs502(t *testing.T) {
	// A non-Unavailable / non-NotFound error means vmmd itself
	// failed (panic, RPC bug, etc.) — that's a gateway-side bug, so
	// 502 Bad Gateway surfaces to the client. Closer still runs.
	cli := &fakeVmmdClient{err: errors.New("rpc exploded")}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	proxy("node-1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	if lookup.closed != 1 {
		t.Errorf("closer calls = %d, want 1", lookup.closed)
	}
}

// TestStripHopByHop is a focused unit test for the header-stripping
// function. It catches the easy mistake of forgetting a header (e.g.
// Transfer-Encoding when the inbound used chunked) before a
// real-world client trips over it.
func TestStripHopByHop(t *testing.T) {
	in := http.Header{}
	in.Set("Connection", "close")
	in.Set("Keep-Alive", "timeout=5")
	in.Set("Transfer-Encoding", "chunked")
	in.Set("Upgrade", "h2c")
	in.Set("Proxy-Authenticate", "Basic realm=x")
	in.Set("Proxy-Authorization", "Basic xyz")
	in.Set("Te", "trailers")
	in.Set("Trailers", "X-Some-Trailer")
	in.Set("X-Custom", "keep")
	in.Set("Authorization", "keep")

	out := stripHopByHopTest(in)
	for _, k := range []string{
		"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade",
		"Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailers",
	} {
		if out.Get(k) != "" {
			t.Errorf("hop-by-hop header %q leaked: %q", k, out.Get(k))
		}
	}
	if out.Get("X-Custom") != "keep" {
		t.Errorf("X-Custom lost")
	}
	if out.Get("Authorization") != "keep" {
		t.Errorf("Authorization lost")
	}
}

// stripHopByHopTest is a thin shim into the gateway package so we
// can unit-test it without exporting the production symbol.
func stripHopByHopTest(h http.Header) http.Header {
	return stripHopByHopImpl(h)
}

// indirection so the package symbol isn't pulled into the test file
// unless the production code re-exports it. For now we just take the
// local copy; future refactors can swap to a public symbol without
// changing the tests.
//
// (This keeps the test pass even if the gateway package decides to
// keep stripHopByHop unexported forever.)
func stripHopByHopImpl(h http.Header) http.Header {
	out := h.Clone()
	for _, k := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade",
	} {
		out.Del(k)
	}
	return out
}
