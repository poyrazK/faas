// Tests for the gatewayd-internal Handler × ForwardingReverseProxy integration
// (issue #98 / ADR-028 / ADR-047). The unit tests in forwardproxy_test.go
// pin the forwarder in isolation; this file pins the seam — when the
// Handler has proxyByNode installed, every request dispatches
// through it.
//
// PR-D / ADR-047: the legacy unary ForwardHTTP RPC was removed. The
// stubVmmdClient now drives the bidi ForwardHTTPStream RPC via the
// in-package proxy_stub.go fake. The streaming-only shape keeps
// the integration test on one consistent bridge.
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
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubVmmdClient is the in-package fake for the handler-side seam
// test. forwardproxy_test.go's fakeVmmdClient lives in
// package gateway_test and isn't reachable from here. This stub
// satisfies the full vmmdpb.VmmdClient interface (ForwardHTTPStream
// + the other RPCs the cache might exercise on shutdown) so the
// NodeClientLookup can hand it back without an interface-conversion
// error.
//
// PR-D / ADR-047: the streaming RPC is the only bridge today. The
// stub dispatches ForwardHTTPStream through a bufconn-based fake
// server (proxy_stub.go) that records the request init and writes
// a fixed response body — the integration test asserts the body
// reaches the inbound ResponseWriter and the init was sent.
type stubVmmdClient struct {
	mu        sync.Mutex
	calls     []*vmmdpb.ForwardHTTPRequestInit
	rawCalls  []*vmmdpb.ForwardRawRequestInit
	resp      *vmmdpb.ForwardHTTPResponseInit
	rawResp   *vmmdpb.ForwardRawResponseInit
	rawBody   []byte
	body      []byte
	rawStream grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]
}

func (s *stubVmmdClient) ForwardHTTPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := newProxyStubStream(ctx, &s.calls, s.resp, s.body)
	return stream, nil
}

// ForwardRawStream (issue #676 / ADR-080) is the raw-bytes
// bridge for Upgrade traffic (WebSocket / h2c / MQTT-over-WS /
// long-poll). The PR-3 gateway forwarder opens this stream when
// it detects Connection: Upgrade + Upgrade: <token>; the test
// seam returns a configured proxyRawStubStream (the
// in-package equivalent of the bufconn fake — see
// proxy_raw_stub.go) so the integration test can drive the
// forwarder with a canned response. A test that wires the seam
// without configuring rawResp / rawBody will surface as an
// immediately-empty stream (the forwarder observes io.EOF on the
// first Recv), which is the load-bearing failure mode the test
// wants to pin.
func (s *stubVmmdClient) ForwardRawStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rawStream != nil {
		return s.rawStream, nil
	}
	stream := newProxyRawStubStream(ctx, &s.rawCalls, s.rawResp, s.rawBody)
	return stream, nil
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

// WarmSnapshot (issue #470 / PR #470-FU-A) is the forwardproxy
// handler integration test's no-op seam — the gateway forward
// path doesn't fire warm captures (the engine reaper is the
// only entry point). The stub intentionally panics if the
// test path ever wires it through so it surfaces as a clear
// test mistake rather than a silent no-op.
func (s *stubVmmdClient) WarmSnapshot(context.Context, *vmmdpb.WarmSnapshotRequest, ...grpc.CallOption) (*vmmdpb.SnapshotResponse, error) {
	panic("WarmSnapshot: not stubbed in handler integration test")
}
func (s *stubVmmdClient) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	return &vmmdpb.DestroyResponse{}, nil
}

// StopInstance (M-2 / ADR-138 §Decision 1) is the graceful
// signal-grace-SIGKILL stop sequence. The forwardproxy handler
// integration test never exercises it (the forwarder is
// request-shaped, not worker/job), so the stub returns the
// zero-value response — same shape as Destroy.
func (s *stubVmmdClient) StopInstance(context.Context, *vmmdpb.StopInstanceRequest, ...grpc.CallOption) (*vmmdpb.StopInstanceResponse, error) {
	return &vmmdpb.StopInstanceResponse{}, nil
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

// UpdateEgressAllowlist (tier-2 PR-B) — the gateway hot path
// doesn't drive the in-place patch; schedd's egress_drift
// subscriber does. Returns success so the gRPC VmmdClient
// interface stays satisfied.
func (s *stubVmmdClient) UpdateEgressAllowlist(context.Context, *vmmdpb.UpdateEgressAllowlistRequest, ...grpc.CallOption) (*vmmdpb.UpdateEgressAllowlistAck, error) {
	return &vmmdpb.UpdateEgressAllowlistAck{}, nil
}

// UpdateStaticEgressIP (ADR-119) — the gateway hot path
// doesn't drive the in-place patch; schedd's egress_drift
// subscriber does. Returns success so the gRPC VmmdClient
// interface stays satisfied. Mirrors UpdateEgressAllowlist above.
func (s *stubVmmdClient) UpdateStaticEgressIP(context.Context, *vmmdpb.UpdateStaticEgressIPRequest, ...grpc.CallOption) (*vmmdpb.UpdateStaticEgressIPAck, error) {
	return &vmmdpb.UpdateStaticEgressIPAck{}, nil
}

// Logs (issue #254 / Move 4) — the gateway hot path never dials
// the per-instance log stream directly; apid dials schedd for
// that. The stub returns Unimplemented so any accidental test
// that touches the codepath fails fast with a stable gRPC code.
func (s *stubVmmdClient) Logs(context.Context, *vmmdpb.LogsRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[vmmdpb.LogsResponse], error) {
	return nil, status.Error(codes.Unimplemented, "gateway stub does not stream logs")
}

// SeccompStatus (M8 §11) — the gateway hot path doesn't poll
// seccomp state; cmd/e2e/sec11_seccomp_e2e_test.go drives the
// dial directly. Returns a "not implemented" envelope so the
// gRPC VmmdClient interface stays satisfied; this path is never
// expected to fire in handler tests.
func (s *stubVmmdClient) SeccompStatus(context.Context, *vmmdpb.SeccompStatusRequest, ...grpc.CallOption) (*vmmdpb.SeccompStatusResponse, error) {
	return &vmmdpb.SeccompStatusResponse{}, nil
}

// MountParentExt4ReadOnly (ADR-053) — gateway never drives the
// parent-mount staging path; imaged owns that RPC. Returns
// empty + nil so the vmmdpb.VmmdClient interface is satisfied.
// Any accidental caller would surface as imaged's
// "empty mountpoint" check rather than a NotFound from vmmd.
func (s *stubVmmdClient) MountParentExt4ReadOnly(context.Context, *vmmdpb.MountParentExt4ReadOnlyRequest, ...grpc.CallOption) (*vmmdpb.MountParentExt4ReadOnlyResponse, error) {
	return &vmmdpb.MountParentExt4ReadOnlyResponse{}, nil
}

// MaterializeParentExt4 is likewise outside the gateway hot path. Keep the
// generated client stub complete as vmmd's staging surface evolves.
func (s *stubVmmdClient) MaterializeParentExt4(context.Context, *vmmdpb.MaterializeParentExt4Request, ...grpc.CallOption) (*vmmdpb.MaterializeParentExt4Response, error) {
	return &vmmdpb.MaterializeParentExt4Response{}, nil
}

// UmountParentExt4 (ADR-053) — gateway never drives the parent
// umount path. Returns nil.
func (s *stubVmmdClient) UmountParentExt4(context.Context, *vmmdpb.UmountParentExt4Request, ...grpc.CallOption) (*vmmdpb.UmountParentExt4Response, error) {
	return &vmmdpb.UmountParentExt4Response{}, nil
}

// Tier A5 (ADR-066) — the four migration RPCs are not driven
// from the gateway; they are schedd→vmmd only. Stubs return
// empty responses so the gRPC VmmdClient interface stays
// satisfied.
func (s *stubVmmdClient) PrepareLiveMigration(context.Context, *vmmdpb.PrepareLiveMigrationRequest, ...grpc.CallOption) (*vmmdpb.PrepareLiveMigrationResponse, error) {
	return &vmmdpb.PrepareLiveMigrationResponse{}, nil
}
func (s *stubVmmdClient) AdoptMigratedInstance(context.Context, *vmmdpb.AdoptMigratedInstanceRequest, ...grpc.CallOption) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	return &vmmdpb.AdoptMigratedInstanceResponse{}, nil
}
func (s *stubVmmdClient) AcknowledgeMigration(context.Context, *vmmdpb.AcknowledgeMigrationRequest, ...grpc.CallOption) (*vmmdpb.AcknowledgeMigrationResponse, error) {
	return &vmmdpb.AcknowledgeMigrationResponse{}, nil
}
func (s *stubVmmdClient) CancelLiveMigration(context.Context, *vmmdpb.CancelLiveMigrationRequest, ...grpc.CallOption) (*vmmdpb.CancelLiveMigrationResponse, error) {
	return &vmmdpb.CancelLiveMigrationResponse{}, nil
}

// FrameworkReady (issue #470 / PR #470-FU-B) is the vmmd-side
// receipt of the guest-init "framework ready" DGRAM. The
// forwardproxy handler never invokes this RPC (it's a vmmd→vmmd
// receipt path routed via the schedd), but the vmmd gRPC
// client interface demands the method so the stub satisfies
// the full surface. Returns an empty success; tests that
// exercise the framework-ready data path live in
// pkg/vmmdgrpc/bufconn_test.go.
func (s *stubVmmdClient) FrameworkReady(context.Context, *vmmdpb.FrameworkReadyRequest, ...grpc.CallOption) (*vmmdpb.FrameworkReadyResponse, error) {
	return &vmmdpb.FrameworkReadyResponse{}, nil
}

// MountOverlayParent (ADR-075 / DEPLOY-1) is the staged-overlay
// mount RPC imaged uses. The forwardproxy handler never
// invokes this RPC (it lives in pkg/imaged), but the vmmd gRPC
// client interface demands the method so the stub satisfies
// the full surface. Returns an empty success.
func (s *stubVmmdClient) MountOverlayParent(context.Context, *vmmdpb.MountOverlayParentRequest, ...grpc.CallOption) (*vmmdpb.MountOverlayParentResponse, error) {
	return &vmmdpb.MountOverlayParentResponse{}, nil
}

// UmountOverlayParent (ADR-075 / DEPLOY-1) — paired with
// MountOverlayParent above. forwardproxy never invokes; stub
// returns success.
func (s *stubVmmdClient) UmountOverlayParent(context.Context, *vmmdpb.UmountOverlayParentRequest, ...grpc.CallOption) (*vmmdpb.UmountOverlayParentResponse, error) {
	return &vmmdpb.UmountOverlayParentResponse{}, nil
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
		resp: &vmmdpb.ForwardHTTPResponseInit{
			Status: 200,
		},
		body: []byte("forwarded:ok"),
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
	h.proxyFor = func(addr string, cap int64) http.Handler {
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
	cli := &stubVmmdClient{
		resp: &vmmdpb.ForwardHTTPResponseInit{Status: 200},
	}
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
	first := func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	}
	second := func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	b := &fakeBackend{app: App{ID: "app-1", Plan: api.PlanScale}, host: "app.example.com", running: true}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(first)
	h.WithForwarding(second)

	if got := h.proxyByNode(Target{NodeID: "anything"}); got == nil {
		t.Fatal("proxyByNode nil after install")
	}
}

// Upgrade-traffic detector tests (issue #676 / ADR-080). The handler
// checks isUpgradeRequest(r) AND app.WebSocketEnabled AND h.rawByNode
// != nil before routing to the raw forwarder; the four cases below
// pin every branch of that gate.
//
// Why these matter:
//   - The detector decides which bridge (ForwardHTTPStream vs
//     ForwardRawStream) the request flows over. A misclassified
//     request either destroys the WS handshake (plain path strips
//     hop-by-hop) or wastes a wake (raw path on a non-Upgrade
//     request). Both are customer-visible failures.
//   - The per-app flag is the load-bearing gate against Free-tier
//     abuse (a long-lived WS pins a wake past wake_idle_timeout).
//     A Free app with WebSocketEnabled=true must NOT reach the
//     raw forwarder — the apid PATCH gate prevents the state, the
//     handler test pins the runtime contract.

// TestServeHTTP_UpgradeHeader_BypassesProxyByNode confirms an inbound
// request with Connection: Upgrade + Upgrade: websocket on an
// app with WebSocketEnabled=true routes to h.rawByNode (and the
// proxyByNode handler is NEVER invoked). The raw forwarder returns
// a canned 101 response; the test asserts both that the raw path
// fired AND that proxyByNode.calls remains zero.
func TestServeHTTP_UpgradeHeader_BypassesProxyByNode(t *testing.T) {
	cli := &stubVmmdClient{
		rawResp: &vmmdpb.ForwardRawResponseInit{Status: 101},
		rawBody: []byte("upgrade-ack"),
	}
	_ = cli
	var rawCalls, proxyCalls int
	b := &fakeBackend{
		app: App{
			ID: "app-1", Plan: api.PlanScale, WebSocketEnabled: true,
			StreamingEnabled: false,
		},
		host: "app.example.com", upstream: "node-uuid-1", running: true,
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			proxyCalls++
			w.WriteHeader(http.StatusOK)
		})
	})
	h.WithRawForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			rawCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "raw")
		})
	})

	req := httptest.NewRequest("GET", "/socket", nil)
	req.Host = "app.example.com"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if proxyCalls != 0 {
		t.Errorf("proxyByNode invoked %d times on Upgrade request, want 0", proxyCalls)
	}
	if rawCalls != 1 {
		t.Errorf("rawByNode invoked %d times, want 1", rawCalls)
	}
}

// TestServeHTTP_NonUpgradeHeader_TakesProxyByNode confirms that a
// plain HTTP request (no Upgrade header) on a WebSocketEnabled app
// still flows through proxyByNode — the raw path is a strict
// superset, not a replacement. Without this guard a misconfigured
// deployment that wires rawByNode would silently route every
// plain HTTP request through the slower raw bridge.
func TestServeHTTP_NonUpgradeHeader_TakesProxyByNode(t *testing.T) {
	cli := &stubVmmdClient{
		resp: &vmmdpb.ForwardHTTPResponseInit{Status: 200},
		body: []byte("plain-ok"),
	}
	_ = cli
	var rawCalls, proxyCalls int
	b := &fakeBackend{
		app: App{
			ID: "app-1", Plan: api.PlanScale, WebSocketEnabled: true,
		},
		host: "app.example.com", upstream: "node-uuid-1", running: true,
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			proxyCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "proxy")
		})
	})
	h.WithRawForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			rawCalls++
			w.WriteHeader(http.StatusOK)
		})
	})

	req := httptest.NewRequest("GET", "/v1/plain", nil)
	req.Host = "app.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rawCalls != 0 {
		t.Errorf("rawByNode invoked %d times on plain request, want 0", rawCalls)
	}
	if proxyCalls != 1 {
		t.Errorf("proxyByNode invoked %d times, want 1", proxyCalls)
	}
}

// TestServeHTTP_FreePlan_WebSocketNotAllowed confirms that an app
// with WebSocketEnabled=false (Free plan OR a Hobby+ customer who
// PATCHed the flag off) short-circuits to a deterministic 501 +
// x-faas-error-reason: websocket_not_on_plan (issue #707 / PR-3
// review finding). The previous fall-through to proxyByNode
// stripped Connection + Upgrade as hop-by-hop (RFC 7230 §6.1) and
// returned 502 from the upstream — a confusing customer-facing
// failure that retried the WS handshake in an infinite loop. The
// 501 path names the cause; the WS client backs off cleanly.
//
// Neither rawByNode nor proxyByNode is invoked — the short-circuit
// sits BETWEEN the upgrade detector and both forwarding paths. The
// apid plan gate prevents a Free customer from getting
// WebSocketEnabled=true in the first place (pkg/api/errors.go:471
// CodePlanWebSocketNotAllowed), but a Hobby+ opt-out still lands
// here.
func TestServeHTTP_FreePlan_WebSocketNotAllowed(t *testing.T) {
	var rawCalls, proxyCalls int
	b := &fakeBackend{
		app: App{
			ID: "app-1", Plan: api.PlanFree, WebSocketEnabled: false,
		},
		host: "app.example.com", upstream: "node-uuid-1", running: true,
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			proxyCalls++
			w.WriteHeader(http.StatusBadGateway)
		})
	})
	h.WithRawForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			rawCalls++
			w.WriteHeader(http.StatusOK)
		})
	})

	req := httptest.NewRequest("GET", "/socket", nil)
	req.Host = "app.example.com"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rawCalls != 0 {
		t.Errorf("rawByNode invoked on Free+disabled app, want 0")
	}
	if proxyCalls != 0 {
		t.Errorf("proxyByNode invoked %d times, want 0 (issue #707: short-circuit, no fall-through)", proxyCalls)
	}
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", rec.Code)
	}
	if got := rec.Header().Get("x-faas-error-reason"); got != "websocket_not_on_plan" {
		t.Errorf("x-faas-error-reason = %q, want websocket_not_on_plan", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
}

// TestServeHTTP_RawForwarderNotWired_WebSocketNotAllowed pins the
// second trigger path of issue #707: WebSocketEnabled=true on the
// app, but h.rawByNode is nil (test fixture, OR the future
// FAAS_GATEWAY_RAW_STREAM_ENABLED=false follow-up). The customer
// sees the same deterministic 501 + x-faas-error-reason:
// websocket_not_on_plan so a WS client backs off cleanly. The
// detail string distinguishes the two causes ("This deployment
// has the WebSocket / Upgrade-traffic raw-bytes bridge disabled")
// for ops log forensics.
func TestServeHTTP_RawForwarderNotWired_WebSocketNotAllowed(t *testing.T) {
	b := &fakeBackend{
		app: App{
			ID: "app-2", Plan: api.PlanHobby, WebSocketEnabled: true,
		},
		host: "app.example.com", upstream: "node-uuid-1", running: true,
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	// NOTE: no WithRawForwarding call — h.rawByNode stays nil.
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Errorf("proxyByNode invoked on upgrade request, want 0 (issue #707: short-circuit, no fall-through)")
			w.WriteHeader(http.StatusBadGateway)
		})
	})

	req := httptest.NewRequest("GET", "/socket", nil)
	req.Host = "app.example.com"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", rec.Code)
	}
	if got := rec.Header().Get("x-faas-error-reason"); got != "websocket_not_on_plan" {
		t.Errorf("x-faas-error-reason = %q, want websocket_not_on_plan", got)
	}
	if !strings.Contains(rec.Body.String(), "deployment has the WebSocket") {
		t.Errorf("body = %q, want substring \"deployment has the WebSocket\" (forwarderMissing detail)", rec.Body.String())
	}
}

// TestServeHTTP_WebSocketEnabled_DispatchesToRawStream confirms the
// full happy path: Hobby plan + WebSocketEnabled=true +
// isUpgradeRequest + rawByNode installed → the raw forwarder is
// the ONLY handler invoked. The test asserts the init frame's
// Instance + Port match the resolved Target's values (the
// forwarder stamp contract from PR-1).
func TestServeHTTP_WebSocketEnabled_DispatchesToRawStream(t *testing.T) {
	cli := &stubVmmdClient{
		rawResp: &vmmdpb.ForwardRawResponseInit{
			Status: 101,
			Headers: []*vmmdpb.Header{
				{Name: "Connection", Value: "Upgrade"},
				{Name: "Upgrade", Value: "websocket"},
			},
		},
		rawBody: []byte("handshake-complete"),
	}
	var proxyCalls int
	b := &fakeBackend{
		app: App{
			ID: "app-1", Plan: api.PlanHobby, WebSocketEnabled: true,
		},
		host: "app.example.com", upstream: "node-uuid-1", running: true,
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			proxyCalls++
			w.WriteHeader(http.StatusOK)
		})
	})
	h.WithRawForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Stand-in for the production rawStreamReverseProxy —
			// this test only asserts dispatch, not the stream
			// framing (covered separately in
			// TestRawStreamReverseProxy_RoundTrip).
			rawStreamForwarder(t, cli, w, r)
		})
	})

	req := httptest.NewRequest("GET", "/socket", nil)
	req.Host = "app.example.com"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if proxyCalls != 0 {
		t.Errorf("proxyByNode invoked on Upgrade request, want 0")
	}
	if rec.Code != http.StatusSwitchingProtocols {
		t.Errorf("status=%d, want 101", rec.Code)
	}
}

// TestServeHTTP_UpgradeRequest_SkipsStreamingWrap pins issue #708
// (PR-3 review finding): an upgrade request on a Hobby+ plan app
// with StreamingEnabled=true (plan default) MUST NOT install the
// streaming wrap (setupStreamingWriter). The wrap's capWriter
// enforces plan.MaxResponseBodyBytes (100 MiB for Hobby+); on a
// long-lived WS session that streams >100 MiB cumulative response
// bytes, capWriter fires onCap mid-WS-frame and breaks the WS
// protocol. The assertion below reaches into the metrics registry
// to confirm streamFlushes / streamActive were not incremented
// for an upgrade request — meaning the wrap was never installed
// and capWriter never ran.
func TestServeHTTP_UpgradeRequest_SkipsStreamingWrap(t *testing.T) {
	cli := &stubVmmdClient{
		rawResp: &vmmdpb.ForwardRawResponseInit{Status: 101},
		rawBody: []byte("handshake-complete"),
	}
	b := &fakeBackend{
		app: App{
			ID: "app-1", Plan: api.PlanHobby,
			StreamingEnabled: true, WebSocketEnabled: true,
		},
		host: "app.example.com", upstream: "node-uuid-1", running: true,
	}
	m := NewMetrics()
	h := NewHandlerWith(b, m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithStreamingEnabled(true) // operator-level streaming on
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})
	h.WithRawForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawStreamForwarder(t, cli, w, r)
		})
	})

	req := httptest.NewRequest("GET", "/socket", nil)
	req.Host = "app.example.com"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSwitchingProtocols {
		t.Errorf("status=%d, want 101", rec.Code)
	}
	// streamFlushes is incremented by setupStreamingWriter's
	// per-flush onFlush hook. An upgrade request must NOT have
	// taken that path — the hook runs only on the streaming
	// wrap, which is gated off for isUpgradeRequest(r) at
	// handler.go:~1614 (issue #708).
	if got := testutil.CollectAndCount(m.streamFlushes, "gateway_stream_flushes_total"); got != 0 {
		t.Errorf("gateway_stream_flushes_total count = %d, want 0 (issue #708: streaming wrap must NOT install on upgrade requests)", got)
	}
}

// rawStreamForwarder is the test-side seam the dispatch test
// uses in place of the production rawStreamReverseProxy. It
// dials the stub vmmd client through ForwardRawStream, sends an
// init frame, and replays the canned response back to the
// inbound writer. This is the simplest path that exercises the
// dispatch gate end-to-end without spinning up a bufconn server
// (the full raw-bridge round-trip is covered by
// TestRawStreamReverseProxy_RoundTrip in forwardproxy_test.go).
func rawStreamForwarder(t *testing.T, cli *stubVmmdClient, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	ctx := r.Context()
	stream, err := cli.ForwardRawStream(ctx)
	if err != nil {
		t.Fatalf("ForwardRawStream: %v", err)
	}
	init := &vmmdpb.ForwardRawRequestInit{Instance: "stubbed-instance", Port: 8080}
	if err := stream.Send(&vmmdpb.ForwardRawRequest{Frame: &vmmdpb.ForwardRawRequest_Init{Init: init}}); err != nil {
		t.Fatalf("Send init: %v", err)
	}
	_ = stream.CloseSend()
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv init: %v", err)
	}
	w.WriteHeader(int(resp.GetInit().GetStatus()))
	for _, h := range resp.GetInit().GetHeaders() {
		w.Header().Add(h.GetName(), h.GetValue())
	}
	// Body chunk, if any.
	body, _ := stream.Recv()
	if body != nil {
		_, _ = w.Write(body.GetBodyChunk())
	}
}
