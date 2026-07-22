// Tests for the gatewayd Handler × ForwardingReverseProxy integration
// (issue #98 / ADR-028). The unit tests in forwardproxy_test.go pin
// the forwarder in isolation; this file pins the seam — when the
// Handler has proxyByNode installed, every request dispatches
// through it. The legacy proxyFor path stays untouched.
//
// What this test exercises:
//   1. proxyByNode != nil + Backend.Target returns a node id → the
//      forwarder is called with that id and the response body is
//      written back to the inbound ResponseWriter.
//   2. proxyByNode nil (legacy path) → proxyFor is called with
//      whatever Target returned (verifies the e2e harness keeps
//      working without the overlay path wired).
//   3. WithForwarding is a fluent setter that doesn't panic on
//      successive calls (idempotent re-install during reload).
//
// Lives in package gateway (not gateway_test) because the seam
// touches unexported fields on Handler (proxyByNode, proxyFor).
// The forwardproxy_test.go fakes are kept in package gateway_test
// on purpose; we re-declare a minimal in-package stub here so the
// integration test compiles standalone.

package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc"
)

// stubVmmdClient is the in-package fake for the handler-side seam
// test. forwardproxy_test.go's fakeVmmdClient lives in
// package gateway_test and isn't reachable from here. This stub
// satisfies the full vmmdpb.VmmdClient interface (ForwardHTTP +
// the other RPCs the cache might exercise on shutdown) so the
// NodeClientLookup can hand it back without an interface-conversion
// error.
type stubVmmdClient struct {
	mu    sync.Mutex
	calls []*vmmdpb.ForwardHTTPRequest
	resp  *vmmdpb.ForwardHTTPResponse
}

func (s *stubVmmdClient) ForwardHTTP(_ context.Context, in *vmmdpb.ForwardHTTPRequest, _ ...grpc.CallOption) (*vmmdpb.ForwardHTTPResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, in)
	return s.resp, nil
}

func (s *stubVmmdClient) CreateFromSnapshot(context.Context, *vmmdpb.CreateFromSnapshotRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	panic("CreateFromSnapshot: not stubbed in handler integration test")
}
func (s *stubVmmdClient) CreateColdBoot(context.Context, *vmmdpb.CreateColdBootRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	panic("CreateColdBoot: not stubbed in handler integration test")
}
func (s *stubVmmdClient) PauseAndSnapshot(context.Context, *vmmdpb.PauseAndSnapshotRequest, ...grpc.CallOption) (*vmmdpb.SnapshotResponse, error) {
	panic("PauseAndSnapshot: not stubbed in handler integration test")
}
func (s *stubVmmdClient) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	return &vmmdpb.DestroyResponse{}, nil
}
func (s *stubVmmdClient) Stats(context.Context, *vmmdpb.StatsRequest, ...grpc.CallOption) (*vmmdpb.StatsResponse, error) {
	return &vmmdpb.StatsResponse{}, nil
}
func (s *stubVmmdClient) Heartbeat(context.Context, *vmmdpb.HeartbeatRequest, ...grpc.CallOption) (*vmmdpb.HeartbeatResponse, error) {
	return &vmmdpb.HeartbeatResponse{}, nil
}
func (s *stubVmmdClient) Ping(context.Context, *vmmdpb.PingRequest, ...grpc.CallOption) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{}, nil
}

// stubLookup matches the NodeClientLookup interface; returns the
// same client for any non-empty id. ok=false on empty (matches the
// defensive 503 contract).
type stubLookup struct {
	cli *stubVmmdClient
}

func (s *stubLookup) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if nodeID == "" {
		return nil, nil, false
	}
	return s.cli, nopCloserFn{}, true
}

type nopCloserFn struct{}

func (nopCloserFn) Close() error { return nil }

// newProxyTestBackend is a small Backend returning a known host +
// a fixed node id, so the handler dispatches straight through the
// forwarder without exercising the wake path. Reuses fakeBackend
// from handler_test.go when possible — but the in-package fake
// already exposes a host set, so the integration tests below wire
// it directly.

func TestHandler_DispatchesThroughProxyByNode(t *testing.T) {
	cli := &stubVmmdClient{
		resp: &vmmdpb.ForwardHTTPResponse{
			Status: 200,
			Body:   []byte("forwarded:ok"),
		},
	}
	lookup := &stubLookup{cli: cli}
	b := &fakeBackend{
		app:      App{ID: "app-1", Plan: api.PlanScale},
		host:     "app.example.com",
		upstream: "node-uuid-1",
		running:  true,
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(ForwardingReverseProxy(lookup, slog.New(slog.NewTextHandler(io.Discard, nil))))

	req := httptest.NewRequest("GET", "/v1/probe?z=1", nil)
	req.Host = "app.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "forwarded:ok") {
		t.Errorf("body=%q, want it to contain the forwarder's response", rec.Body.String())
	}
	if len(cli.calls) != 1 {
		t.Fatalf("forwarder called %d times, want 1", len(cli.calls))
	}
}

func TestHandler_WithoutForwardingFallsBackToProxyFor(t *testing.T) {
	called := false
	b := &fakeBackend{
		app:      App{ID: "app-1", Plan: api.PlanScale},
		host:     "app.example.com",
		upstream: "addr-1",
		running:  true,
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.proxyFor = func(addr string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "addr="+addr)
		})
	}

	req := httptest.NewRequest("GET", "/v1/probe", nil)
	req.Host = "app.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("proxyFor not invoked on the legacy path")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "addr=addr-1") {
		t.Errorf("body=%q, want it to mention the legacy addr", rec.Body.String())
	}
}

func TestHandler_LookupMissStill404sBeforeProxy(t *testing.T) {
	cli := &stubVmmdClient{resp: &vmmdpb.ForwardHTTPResponse{Status: 200}}
	lookup := &stubLookup{cli: cli}
	b := &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale}, host: "app.example.com", running: true}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(ForwardingReverseProxy(lookup, slog.New(slog.NewTextHandler(io.Discard, nil))))
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
	if len(cli.calls) != 0 {
		t.Errorf("forwarder called on Lookup miss: %d calls", len(cli.calls))
	}
}

func TestHandler_WithForwardingIdempotent(t *testing.T) {
	first := func(string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	}
	second := func(string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	b := &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale}, host: "app.example.com", running: true}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(first)
	h.WithForwarding(second)

	if got := h.proxyByNode("anything"); got == nil {
		t.Fatal("proxyByNode nil after install")
	}
}
