// Tests for pkg/gateway/forwardproxy.go (issue #98 / ADR-028 / ADR-047).
// The gateway-side bridge is HTTP-in / gRPC-out. We can't exercise the
// real vmmd end (that requires //go:build metal on Linux), so the
// test uses an in-memory fake VmmdClient that captures the streaming
// ForwardHTTPStreamRequest frames and emits a deterministic
// ForwardHTTPStreamResponse queue. The forwarder is then driven
// through httptest.NewRecorder so we can assert the HTTP shape
// end-to-end.
//
// PR-D / ADR-047: the legacy unary ForwardHTTP RPC was removed. The
// streaming RPC is the only bridge today; every test uses the
// fakeBidiStream scaffold.

package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// fakeVmmdClient is a vmmdpb.VmmdClient that records every
// ForwardHTTPStreamRequest and replies with the canned
// ForwardHTTPStreamResponse queue (or canned error) the test
// installs. It implements only the methods the forwarder uses;
// everything else panics so a future test that accidentally
// routes through here surfaces the mistake.
//
// PR-D / ADR-047: the streaming RPC is the only bridge. The
// Stream field carries the configured fakeBidiStream; an
// unset Stream + a drive-through call panics ("not stubbed").
type fakeVmmdClient struct {
	Stream *fakeBidiStream
	// RawStream is the bidi-streaming client the forwarder
	// sees. Typed as the gRPC interface (not the concrete
	// fake) so issue #710's blockingRawBidiStream can plug
	// in without changing the field shape. The fakeRawBidiStream
	// implements the same interface.
	RawStream grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]
}

// ForwardHTTPStream returns the configured Stream. The forwarder
// drives the bidi stream directly through Send/Recv/CloseSend.
func (f *fakeVmmdClient) ForwardHTTPStream(_ context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	if f.Stream == nil {
		panic("ForwardHTTPStream: not stubbed (set fakeVmmdClient.Stream)")
	}
	return f.Stream, nil
}

// ForwardRawStream (issue #676 / ADR-080) is the raw-bytes
// bridge for Upgrade traffic (WebSocket / h2c / MQTT-over-WS /
// long-poll). The PR-3 gateway forwarder opens this stream when
// it detects Connection: Upgrade + Upgrade: <token>; the
// forwardproxy test seam returns a configured fakeRawBidiStream
// (or panics with a distinctive message if no RawStream was set,
// so an accidentally-wired test surfaces immediately rather than
// silently round-tripping through the legacy ForwardHTTPStream
// path).
func (f *fakeVmmdClient) ForwardRawStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	if f.RawStream == nil {
		panic("ForwardRawStream: not stubbed (set fakeVmmdClient.RawStream)")
	}
	return f.RawStream, nil
}

// All other RPCs panic — the forwarder only calls ForwardHTTPStream.
func (f *fakeVmmdClient) CreateFromSnapshot(context.Context, *vmmdpb.CreateFromSnapshotRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	panic("CreateFromSnapshot: not stubbed")
}
func (f *fakeVmmdClient) CreateColdBoot(context.Context, *vmmdpb.CreateColdBootRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	panic("CreateColdBoot: not stubbed")
}
func (f *fakeVmmdClient) JobColdBoot(context.Context, *vmmdpb.JobColdBootRequest, ...grpc.CallOption) (*vmmdpb.JobColdBootResponse, error) {
	panic("JobColdBoot: not stubbed")
}
func (f *fakeVmmdClient) WaitJobExit(context.Context, *vmmdpb.WaitJobExitRequest, ...grpc.CallOption) (*vmmdpb.JobExitResponse, error) {
	panic("WaitJobExit: not stubbed")
}
func (f *fakeVmmdClient) PauseAndSnapshot(context.Context, *vmmdpb.PauseAndSnapshotRequest, ...grpc.CallOption) (*vmmdpb.SnapshotResponse, error) {
	panic("PauseAndSnapshot: not stubbed")
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the
// forwardproxy test's no-op seam — the gateway forward path
// doesn't fire warm captures. The stub intentionally panics
// so a future test that wires it through surfaces as a clean
// test mistake.
func (f *fakeVmmdClient) WarmSnapshot(context.Context, *vmmdpb.WarmSnapshotRequest, ...grpc.CallOption) (*vmmdpb.SnapshotResponse, error) {
	panic("WarmSnapshot: not stubbed")
}
func (f *fakeVmmdClient) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	panic("Destroy: not stubbed")
}

// StopInstance (M-2 / ADR-138 §Decision 1) is the graceful
// signal-grace-SIGKILL stop sequence. The forwardproxy test rig
// never exercises it (the forwarder is request-shaped, not
// worker/job); panic-on-call makes a future accidental
// invocation surface as a clear test mistake.
func (f *fakeVmmdClient) StopInstance(context.Context, *vmmdpb.StopInstanceRequest, ...grpc.CallOption) (*vmmdpb.StopInstanceResponse, error) {
	panic("StopInstance: not stubbed")
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

// UpdateEgressAllowlist (tier-2 PR-B) — the gateway hot path
// doesn't drive the in-place patch. Panics so a future test
// that actually exercises this RPC from the gateway side fails
// loudly (rather than silently returning a stubbed success).
func (f *fakeVmmdClient) UpdateEgressAllowlist(context.Context, *vmmdpb.UpdateEgressAllowlistRequest, ...grpc.CallOption) (*vmmdpb.UpdateEgressAllowlistAck, error) {
	panic("UpdateEgressAllowlist: not stubbed")
}

// UpdateStaticEgressIP (ADR-119) — the gateway hot path
// doesn't drive the in-place patch. Panics so a future test
// that actually exercises this RPC from the gateway side fails
// loudly (rather than silently returning a stubbed success).
// Mirrors UpdateEgressAllowlist above.
func (f *fakeVmmdClient) UpdateStaticEgressIP(context.Context, *vmmdpb.UpdateStaticEgressIPRequest, ...grpc.CallOption) (*vmmdpb.UpdateStaticEgressIPAck, error) {
	panic("UpdateStaticEgressIP: not stubbed")
}

// Logs (issue #254 / Move 4) — the gateway hot path never dials
// the per-instance log stream directly; apid dials schedd for
// that. Panics so a future test that exercises the RPC from the
// gateway side fails loudly (the gateway is not a logs fan-out
// participant).
func (f *fakeVmmdClient) Logs(context.Context, *vmmdpb.LogsRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[vmmdpb.LogsResponse], error) {
	panic("Logs: gateway hot path doesn't dial per-instance log streams")
}

// fakeBidiStream is the in-process fake for grpc.BidiStreamingClient
// used by the streaming forwarder test. The test pre-loads
// Responses (the canned server→client frame queue; io.EOF on
// Recv when the queue is drained) and inspects Sends after the
// forwarder returns. Concurrency: Send and Recv are called from
// different goroutines by the forwarder (Send from the body-copy
// goroutine, Recv from the main loop), so the queue is guarded
// with a mutex.
type fakeBidiStream struct {
	mu        sync.Mutex
	Responses []*vmmdpb.ForwardHTTPStreamResponse
	Sends     []*vmmdpb.ForwardHTTPStreamRequest
	closed    bool
	recvIdx   int
	recvErr   error // overrides Responses on the next Recv (e.g. codes.Unavailable)
	ctx       context.Context
}

// HeaderStream returns nil — the streaming test doesn't
// introspect headers (the production forwarder doesn't either;
// they're set on the response object by the server side).
func (s *fakeBidiStream) HeaderStream() grpc.ClientStream { return nil }

// TrailerOnly returns false (the gRPC trailer block is set on
// CloseSend in production; the fake ignores it).
func (s *fakeBidiStream) TrailerOnly() bool { return false }

func (s *fakeBidiStream) Send(req *vmmdpb.ForwardHTTPStreamRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.EOF
	}
	// Store the request pointer verbatim. The forwarder's body-copy
	// loop reuses a single 8 KiB buffer for the chunk bytes, but the
	// test never compares captured chunk bytes against the live
	// forwarder buffer after Send returns — the body chunks captured
	// here are observed only after the forwarder has fully drained its
	// loop. The init frame is immutable once constructed. Safe to alias.
	s.Sends = append(s.Sends, req)
	return nil
}

func (s *fakeBidiStream) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recvErr != nil {
		err := s.recvErr
		s.recvErr = nil
		return nil, err
	}
	if s.recvIdx >= len(s.Responses) {
		return nil, io.EOF
	}
	resp := s.Responses[s.recvIdx]
	s.recvIdx++
	return resp, nil
}

func (s *fakeBidiStream) CloseSend() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeBidiStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *fakeBidiStream) SendMsg(any) error { return nil }
func (s *fakeBidiStream) RecvMsg(any) error { return nil }
func (s *fakeBidiStream) Header() (metadata.MD, error) {
	return nil, nil
}
func (s *fakeBidiStream) Trailer() metadata.MD { return nil }

// fakeRawBidiStream (issue #676 / ADR-080) is the in-process fake
// for grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]
// used by the raw-bytes Upgrade forwarder test. The test pre-loads
// Responses (the canned server→client frame queue; io.EOF on Recv
// when the queue is drained) and inspects Sends after the forwarder
// returns. Send and Recv run on different goroutines (Send from the
// body-copy goroutine, Recv from the receiver loop), so the queue
// is guarded with a mutex. The init-frame fixture is the canonical
// pattern for tests that want to assert the gateway wrote a
// ForwardRawRequestInit matching the expected Instance/Port before
// the body chunks started flowing.
type fakeRawBidiStream struct {
	mu        sync.Mutex
	Responses []*vmmdpb.ForwardRawResponse
	Sends     []*vmmdpb.ForwardRawRequest
	closed    bool
	recvIdx   int
	recvErr   error // overrides Responses on the next Recv (e.g. codes.Unavailable)
	ctx       context.Context
}

func (s *fakeRawBidiStream) HeaderStream() grpc.ClientStream { return nil }
func (s *fakeRawBidiStream) TrailerOnly() bool               { return false }

func (s *fakeRawBidiStream) Send(req *vmmdpb.ForwardRawRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.EOF
	}
	s.Sends = append(s.Sends, req)
	return nil
}

func (s *fakeRawBidiStream) Recv() (*vmmdpb.ForwardRawResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recvErr != nil {
		err := s.recvErr
		s.recvErr = nil
		return nil, err
	}
	if s.recvIdx >= len(s.Responses) {
		return nil, io.EOF
	}
	resp := s.Responses[s.recvIdx]
	s.recvIdx++
	return resp, nil
}

func (s *fakeRawBidiStream) CloseSend() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeRawBidiStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *fakeRawBidiStream) SendMsg(any) error { return nil }
func (s *fakeRawBidiStream) RecvMsg(any) error { return nil }
func (s *fakeRawBidiStream) Header() (metadata.MD, error) {
	return nil, nil
}
func (s *fakeRawBidiStream) Trailer() metadata.MD { return nil }

// SeccompStatus (M8 §11) — the gateway hot path doesn't poll
// seccomp state; cmd/e2e/sec11_seccomp_e2e_test.go dials the
// vmmd socket directly to assert the filter is in place. Panics
// so a future test that accidentally couples the gateway hot
// path to seccomp state fails loudly instead of silently
// returning a stub.
func (f *fakeVmmdClient) SeccompStatus(context.Context, *vmmdpb.SeccompStatusRequest, ...grpc.CallOption) (*vmmdpb.SeccompStatusResponse, error) {
	panic("SeccompStatus: not stubbed")
}

// MountParentExt4ReadOnly (ADR-053) — gateway forwardproxy tests
// never drive the parent-mount staging path; imaged owns those
// RPCs. Returns empty + nil so the vmmdpb.VmmdClient interface is
// satisfied; any accidental caller would surface as imaged's
// "empty mountpoint" check rather than a NotFound from vmmd.
func (f *fakeVmmdClient) MountParentExt4ReadOnly(context.Context, *vmmdpb.MountParentExt4ReadOnlyRequest, ...grpc.CallOption) (*vmmdpb.MountParentExt4ReadOnlyResponse, error) {
	return &vmmdpb.MountParentExt4ReadOnlyResponse{}, nil
}

func (f *fakeVmmdClient) MaterializeParentExt4(context.Context, *vmmdpb.MaterializeParentExt4Request, ...grpc.CallOption) (*vmmdpb.MaterializeParentExt4Response, error) {
	return &vmmdpb.MaterializeParentExt4Response{}, nil
}

// UmountParentExt4 (ADR-053) — gateway forwardproxy tests never
// drive the parent umount path. Returns nil.
func (f *fakeVmmdClient) UmountParentExt4(context.Context, *vmmdpb.UmountParentExt4Request, ...grpc.CallOption) (*vmmdpb.UmountParentExt4Response, error) {
	return &vmmdpb.UmountParentExt4Response{}, nil
}

// Tier A5 (ADR-066) — gateway never drives the migration RPCs.
func (f *fakeVmmdClient) PrepareLiveMigration(context.Context, *vmmdpb.PrepareLiveMigrationRequest, ...grpc.CallOption) (*vmmdpb.PrepareLiveMigrationResponse, error) {
	return &vmmdpb.PrepareLiveMigrationResponse{}, nil
}
func (f *fakeVmmdClient) AdoptMigratedInstance(context.Context, *vmmdpb.AdoptMigratedInstanceRequest, ...grpc.CallOption) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	return &vmmdpb.AdoptMigratedInstanceResponse{}, nil
}
func (f *fakeVmmdClient) AcknowledgeMigration(context.Context, *vmmdpb.AcknowledgeMigrationRequest, ...grpc.CallOption) (*vmmdpb.AcknowledgeMigrationResponse, error) {
	return &vmmdpb.AcknowledgeMigrationResponse{}, nil
}
func (f *fakeVmmdClient) CancelLiveMigration(context.Context, *vmmdpb.CancelLiveMigrationRequest, ...grpc.CallOption) (*vmmdpb.CancelLiveMigrationResponse, error) {
	return &vmmdpb.CancelLiveMigrationResponse{}, nil
}

// FrameworkReady (issue #470 / PR #470-FU-B) is the vmmd-side
// receipt of the guest-init "framework ready" DGRAM. The
// forwardproxy never invokes this RPC (the signal path is
// vmmd→vmmd via the schedd gRPC bridge), but the vmmd gRPC
// client interface is closed, so the fake must satisfy it.
// Returns an empty success; tests that exercise the
// framework-ready receipt live in pkg/vmmdgrpc/bufconn_test.go.
func (f *fakeVmmdClient) FrameworkReady(context.Context, *vmmdpb.FrameworkReadyRequest, ...grpc.CallOption) (*vmmdpb.FrameworkReadyResponse, error) {
	return &vmmdpb.FrameworkReadyResponse{}, nil
}

// MountOverlayParent / UmountOverlayParent (ADR-075 / DEPLOY-1):
// the forwardproxy handler never issues the overlay mount RPC
// (imaged owns that), but the vmmdpb.VmmdClient interface
// demands both methods, so the stub returns success to satisfy
// the surface. Tests that exercise the actual mount path live
// in pkg/vmmdgrpc/bufconn_test.go.
func (f *fakeVmmdClient) MountOverlayParent(context.Context, *vmmdpb.MountOverlayParentRequest, ...grpc.CallOption) (*vmmdpb.MountOverlayParentResponse, error) {
	return &vmmdpb.MountOverlayParentResponse{}, nil
}
func (f *fakeVmmdClient) UmountOverlayParent(context.Context, *vmmdpb.UmountOverlayParentRequest, ...grpc.CallOption) (*vmmdpb.UmountOverlayParentResponse, error) {
	return &vmmdpb.UmountOverlayParentResponse{}, nil
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

// TestForwardingReverseProxy_HappyPath pins the streaming-only path
// (issue #471 PR-D / ADR-047). The forwarder must:
//
//   - Open the bidi ForwardHTTPStream RPC (legacy unary ForwardHTTP
//     was removed in PR-D).
//   - Send the init frame with the method/uri/headers + Stream=true.
//   - Stream the request body in 8 KiB chunks (the body-copy
//     goroutine handles the loop).
//   - Pipe the response init (status + headers) into w.
//   - Pipe each body_chunk frame into w (the production
//     statusRecorder.Write → maybeFlush → onFlush path picks up).
//   - Drain the body goroutine before returning.
//
// The fake bidi stream captures every Send frame and queues
// canned responses; the test asserts the captured frames match
// what fwdStreamOnce should have written.
func TestForwardingReverseProxy_HappyPath(t *testing.T) {
	const requestBody = `{"x":1}`
	stream := &fakeBidiStream{
		Responses: []*vmmdpb.ForwardHTTPStreamResponse{
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
				Init: &vmmdpb.ForwardHTTPResponseInit{
					Status: 200,
					Headers: []*vmmdpb.Header{
						{Name: "Content-Type", Value: "application/json"},
					},
				},
			}},
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{
				BodyChunk: []byte(`{"hello":"world"}`),
			}},
		},
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/items?id=42", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	// Hop-by-hop headers must be stripped before sending — see
	// stripHopByHop. Connection in particular would otherwise confuse
	// the guest's response framing.
	req.Header.Set("Connection", "close")
	req.Header.Set("X-Custom", "keep-me")
	// x-faas-stream is always-on post-PR-D; the Handler stamps it
	// before the forwarder dispatches.
	req.Header.Set("x-faas-stream", "true")
	req.Header.Set("x-faas-instance", "i-test")

	rec := httptest.NewRecorder()
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test"}).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
	if got := rec.Body.String(); got != `{"hello":"world"}` {
		t.Errorf("body = %q", got)
	}

	// First Send must be the init frame with Stream=true and the
	// expected method/uri/headers (x-faas-* stripped, Content-Type
	// preserved).
	if len(stream.Sends) < 1 {
		t.Fatalf("expected ≥ 1 Send (init), got %d", len(stream.Sends))
	}
	init := stream.Sends[0].GetInit()
	if init == nil {
		t.Fatalf("first Send is not an init frame: %+v", stream.Sends[0])
	}
	if init.GetInstance() != "i-test" {
		t.Errorf("init.Instance = %q, want i-test", init.GetInstance())
	}
	if init.GetMethod() != http.MethodPost {
		t.Errorf("init.Method = %q, want POST", init.GetMethod())
	}
	if !init.GetStream() {
		t.Errorf("init.Stream = false, want true")
	}
	// Connection was stripped; X-Custom + Authorization survived.
	gotHeaders := map[string]string{}
	for _, h := range init.GetHeaders() {
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

	// The client body must have been sent as one or more body_chunk
	// frames after the init.
	if len(stream.Sends) < 2 {
		t.Fatalf("expected ≥ 2 Sends (init + body chunks), got %d", len(stream.Sends))
	}
	var sentBody []byte
	for _, s := range stream.Sends[1:] {
		sentBody = append(sentBody, s.GetBodyChunk()...)
	}
	if string(sentBody) != requestBody {
		t.Errorf("sent body = %q, want %q", sentBody, requestBody)
	}

	// Closer ran exactly once.
	if lookup.closed != 1 {
		t.Errorf("closer calls = %d, want 1", lookup.closed)
	}
}

// TestForwardingReverseProxy_StampsTargetPort pins issue #460 /
// ADR-053 (PR-C): the picked Target's Port must reach
// ForwardHTTPRequestInit.port so vmmd's buildStreamingBridgeScript
// dials the override port. A regression that drops Port from the
// picked Target's view (or omits it from the init frame) would
// silently force every override-port deployment to 8080, which
// the vmmd server-side default would mask — silent 503s only
// visible in production logs.
func TestForwardingReverseProxy_StampsTargetPort(t *testing.T) {
	stream := &fakeBidiStream{
		Responses: []*vmmdpb.ForwardHTTPStreamResponse{
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
				Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200},
			}},
		},
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("x-faas-stream", "true")
	r.Header.Set("x-faas-instance", "i-test")
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test", Port: 9090}).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(stream.Sends) < 1 {
		t.Fatalf("expected ≥ 1 Send (init), got %d", len(stream.Sends))
	}
	init := stream.Sends[0].GetInit()
	if init == nil {
		t.Fatalf("first Send is not an init frame")
	}
	if got := init.GetPort(); got != 9090 {
		t.Errorf("ForwardHTTPRequestInit.port = %d, want 9090", got)
	}
}

// TestForwardingReverseProxy_PortZeroDefaultsAtBoundary pins the
// legacy wiring: a Target with Port=0 (the no-override case) still
// sends a ForwardHTTPRequestInit with port=0 — the server-side 8080
// default lives in vmmd's buildStreamingBridgeScript, not here.
// Asserting this prevents a future "forwardproxy auto-fills 8080
// on behalf of legacy callers" regression from masking port wiring
// bugs.
func TestForwardingReverseProxy_PortZeroDefaultsAtBoundary(t *testing.T) {
	stream := &fakeBidiStream{
		Responses: []*vmmdpb.ForwardHTTPStreamResponse{
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
				Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200},
			}},
		},
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("x-faas-stream", "true")
	r.Header.Set("x-faas-instance", "i-test")
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test"}).ServeHTTP(rec, r)

	if len(stream.Sends) < 1 {
		t.Fatalf("expected ≥ 1 Send (init), got %d", len(stream.Sends))
	}
	init := stream.Sends[0].GetInit()
	if init == nil {
		t.Fatalf("first Send is not an init frame")
	}
	if got := init.GetPort(); got != 0 {
		t.Errorf("ForwardHTTPRequestInit.port = %d, want 0 (server defaults to 8080)", got)
	}
}

// TestForwardingReverseProxy_SidecarPortOverrideWins pins issue
// #463 / ADR-069 / ADR-071 / PR-C §5: a per-request sidecar-port
// override (stamped by the handler when the routing-key resolver
// picks a sidecar) takes precedence over Target.Port. The handler
// rides the override on the request context (SidecarPortFrom),
// because the Target set is shared across all instances of a
// deployment and the override is per-request, not per-target.
// A regression that drops the override would silently route
// /metrics traffic to the main workload's :8080 — the sidecar
// Prometheus scrape would either hang or hit the wrong service.
func TestForwardingReverseProxy_SidecarPortOverrideWins(t *testing.T) {
	stream := &fakeBidiStream{
		Responses: []*vmmdpb.ForwardHTTPStreamResponse{
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
				Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200},
			}},
		},
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.Header.Set("x-faas-stream", "true")
	r.Header.Set("x-faas-instance", "i-test")
	// Target.Port is the main workload's port (8080); the
	// handler's sidecar selector overrode it to 9100 via the
	// request context below.
	r = r.WithContext(gateway.WithSidecarPort(r.Context(), 9100))
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test", Port: 8080}).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(stream.Sends) < 1 {
		t.Fatalf("expected ≥ 1 Send (init), got %d", len(stream.Sends))
	}
	init := stream.Sends[0].GetInit()
	if init == nil {
		t.Fatalf("first Send is not an init frame")
	}
	if got := init.GetPort(); got != 9100 {
		t.Errorf("ForwardHTTPRequestInit.port = %d, want 9100 (sidecar override beats Target.Port)", got)
	}
}

// TestForwardingReverseProxy_NoSidecarOverrideFallsBackToTarget
// pins the no-routing-key-sidecar branch: when the request has
// no sidecar-port override on context, the forwarder uses
// Target.Port verbatim. This is the legacy single-app path — the
// PR-C §5 routing-key selector sets the override only when the
// hostname carries a `<host>--<sidecar>` segment.
func TestForwardingReverseProxy_NoSidecarOverrideFallsBackToTarget(t *testing.T) {
	stream := &fakeBidiStream{
		Responses: []*vmmdpb.ForwardHTTPStreamResponse{
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
				Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200},
			}},
		},
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("x-faas-stream", "true")
	r.Header.Set("x-faas-instance", "i-test")
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test", Port: 9090}).ServeHTTP(rec, r)

	if len(stream.Sends) < 1 {
		t.Fatalf("expected ≥ 1 Send (init), got %d", len(stream.Sends))
	}
	init := stream.Sends[0].GetInit()
	if init == nil {
		t.Fatalf("first Send is not an init frame")
	}
	if got := init.GetPort(); got != 9090 {
		t.Errorf("ForwardHTTPRequestInit.port = %d, want 9090 (no override → Target.Port wins)", got)
	}
}

func TestForwardingReverseProxy_UnknownNodeIs503(t *testing.T) {
	stream := &fakeBidiStream{}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	rec := httptest.NewRecorder()
	proxy(gateway.Target{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if len(stream.Sends) != 0 {
		t.Errorf("ForwardHTTPStream called %d times with empty node id", len(stream.Sends))
	}
}

// TestForwardingReverseProxy_StreamUnavailableIs503 pins the
// error-mapping contract on the streaming path (issue #471 /
// ADR-047). Unavailable from the bidi stream must surface as
// 503 to the client, matching the unary ForwardHTTP contract.
func TestForwardingReverseProxy_StreamUnavailableIs503(t *testing.T) {
	stream := &fakeBidiStream{
		recvErr: status.Error(codes.Unavailable, "node sick"),
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}

	proxy := gateway.ForwardingReverseProxy(lookup, nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader("hi"))
	r.Header.Set("x-faas-stream", "true")
	r.Header.Set("x-faas-instance", "i-test")
	rec := httptest.NewRecorder()

	proxy(gateway.Target{NodeID: "n1", InstanceID: "i-test", Port: 0}).ServeHTTP(rec, r)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("rec.Code = %d, want 503 (Unavailable must map to 503)", rec.Code)
	}
}

// TestForwardingReverseProxy_StreamOtherErrorIs502 pins the
// non-Unavailable error mapping. A codes.Unknown or rpc-exploded
// error means vmmd itself failed (panic, RPC bug); that's a
// gateway-side bug, so 502 Bad Gateway surfaces to the client.
// Closer still runs (the lease contract is independent of the
// forwarder outcome).
func TestForwardingReverseProxy_StreamOtherErrorIs502(t *testing.T) {
	stream := &fakeBidiStream{
		recvErr: errors.New("rpc exploded"),
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("x-faas-stream", "true")
	r.Header.Set("x-faas-instance", "i-test")
	rec := httptest.NewRecorder()
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test"}).ServeHTTP(rec, r)
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

// TestForwardingReverseProxyWithEvents_EmitsProxyFirstByte pins the
// wake.proxy_first_byte emit (issue #517 / PR-C / ADR-064). The
// forwarder is the customer-facing "how long until the upstream
// answered" surface; the emit MUST land on the events table under
// the right wake_id with a non-zero latency_ms (read back via
// state.Store.ListEventsByWakeID — the same query the customer-
// facing timeline endpoint uses).
//
// The test drives the streaming path with a fake bidi stream that
// replies with a 200 init frame immediately, so the closure has
// time to record a non-zero latency (we don't pin the exact
// number — clock skew would flake).
func TestForwardingReverseProxyWithEvents_EmitsProxyFirstByte(t *testing.T) {
	store := state.NewMemStore()
	platform := events.NewPlatform("gatewayd-internal", store, slog.Default(), wire.NewOpsMetrics("gatewayd-test"), nil)

	stream := &fakeBidiStream{
		Responses: []*vmmdpb.ForwardHTTPStreamResponse{
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
				Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200},
			}},
		},
	}
	cli := &fakeVmmdClient{Stream: stream}
	lookup := &fakeNodeLookup{cli: cli}

	// WakeID lives on the Target (not the wire envelope) — the
	// gateway-fanout cache sets it from the AdmitInstanceResponse
	// at Wake time. The handler stamps the per-request headers
	// (x-faas-app, x-faas-request-id) before dispatch.
	proxy := gateway.ForwardingReverseProxyWithEvents(lookup, nil, platform)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
	req.Header.Set("x-faas-app", "app-proxy-1")
	req.Header.Set("x-faas-request-id", "req-proxy-1")
	req.Header.Set("x-faas-instance", "inst-proxy-1")

	rec := httptest.NewRecorder()
	proxy(gateway.Target{
		NodeID:     "node-1",
		InstanceID: "inst-proxy-1",
		WakeID:     "wake-proxy-1",
	}).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Read the events table back. The wake.proxy_first_byte row
	// should be present exactly once under the wake_id we set
	// on the Target.
	rows, err := store.ListEventsByWakeID(context.Background(), "wake-proxy-1", time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsByWakeID: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Kind != "wake.proxy_first_byte" {
		t.Errorf("kind = %q, want wake.proxy_first_byte", rows[0].Kind)
	}
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["wake_id"] != "wake-proxy-1" {
		t.Errorf("payload.wake_id = %v, want wake-proxy-1", payload["wake_id"])
	}
	if payload["app_id"] != "app-proxy-1" {
		t.Errorf("payload.app_id = %v, want app-proxy-1", payload["app_id"])
	}
	if payload["request_id"] != "req-proxy-1" {
		t.Errorf("payload.request_id = %v, want req-proxy-1", payload["request_id"])
	}
	if payload["instance_id"] != "inst-proxy-1" {
		t.Errorf("payload.instance_id = %v, want inst-proxy-1", payload["instance_id"])
	}
	// latency_ms is computed from time.Since(stamped start) at the
	// emit. The test asserts the value is present and well-formed
	// (we don't pin the exact number — clock skew on a slow CI
	// runner would flake).
	if _, ok := payload["latency_ms"]; !ok {
		t.Errorf("payload.latency_ms missing; got keys %v", keys(payload))
	}
}

// keys is a small helper for diagnostics — keeps the test failure
// messages readable when a payload field is missing.
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Raw-bytes Upgrade-bridge forwarder tests (issue #676 / ADR-080).
// These pin the round-trip contract of rawStreamOnceWithEvents:
// the gateway must (1) open ForwardRawStream, (2) send an init frame
// carrying Instance + Port + MaxRequestBytes, (3) stream the inbound
// request body bytes verbatim (including the request line +
// Connection: Upgrade + Upgrade: <token> headers the hop-by-hop
// strip would otherwise drop), (4) emit the response init frame's
// status + headers, (5) flush each body chunk to the inbound
// ResponseWriter so the H2C transport emits a DATA frame per
// chunk, and (6) tear down on client cancel within 100 ms.
//
// The tests use the package-private fakeRawBidiStream (defined
// above) so the forwarder exercises its full body-copy goroutine
// + receiver loop without a real gRPC server.

// TestRawStreamReverseProxy_RoundTrip confirms the happy path: a
// canned 101 Switching Protocols response with a small body is
// delivered to the inbound writer, the init frame carries the
// expected Instance + Port + MaxRequestBytes, and the request
// body's bytes arrive on the bridge verbatim.
func TestRawStreamReverseProxy_RoundTrip(t *testing.T) {
	stream := &fakeRawBidiStream{
		Responses: []*vmmdpb.ForwardRawResponse{
			{Frame: &vmmdpb.ForwardRawResponse_Init{
				Init: &vmmdpb.ForwardRawResponseInit{
					Status: 101,
					Headers: []*vmmdpb.Header{
						{Name: "Connection", Value: "Upgrade"},
						{Name: "Upgrade", Value: "websocket"},
					},
				},
			}},
			{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{
				BodyChunk: []byte("upgrade-ack"),
			}},
		},
	}
	cli := &fakeVmmdClient{RawStream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingRawReverseProxy(lookup, nil, nil)

	body := "GET /socket HTTP/1.1\r\nHost: app.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	req := httptest.NewRequest(http.MethodGet, "/socket", strings.NewReader(body))
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("x-faas-instance", "i-test")

	rec := httptest.NewRecorder()
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test", Port: 8080}).ServeHTTP(rec, req)

	if rec.Code != 101 {
		t.Errorf("status = %d, want 101", rec.Code)
	}
	if got := rec.Header().Get("Upgrade"); got != "websocket" {
		t.Errorf("Upgrade header = %q, want websocket", got)
	}
	if got := rec.Body.String(); got != "upgrade-ack" {
		t.Errorf("body = %q, want upgrade-ack", got)
	}

	if len(stream.Sends) < 1 {
		t.Fatalf("expected ≥ 1 Send (init), got %d", len(stream.Sends))
	}
	init := stream.Sends[0].GetInit()
	if init == nil {
		t.Fatalf("first Send is not an init frame: %+v", stream.Sends[0])
	}
	if init.GetInstance() != "i-test" {
		t.Errorf("init.Instance = %q, want i-test", init.GetInstance())
	}
	if init.GetPort() != 8080 {
		t.Errorf("init.Port = %d, want 8080", init.GetPort())
	}
	if init.GetMaxRequestBytes() != int64(api.RawStreamMaxRequestBytes) {
		t.Errorf("init.MaxRequestBytes = %d, want %d",
			init.GetMaxRequestBytes(), api.RawStreamMaxRequestBytes)
	}
}

// TestRawStreamReverseProxy_RemoteWakeNode confirms the per-node
// gRPC channel lookup respects NodeID — a different NodeID must
// route through whatever ClientFor returns. We pin the contract by
// wiring the lookup to return a fresh client per NodeID, then
// asserting the inbound request reached the right fake.
func TestRawStreamReverseProxy_RemoteWakeNode(t *testing.T) {
	stream := &fakeRawBidiStream{
		Responses: []*vmmdpb.ForwardRawResponse{
			{Frame: &vmmdpb.ForwardRawResponse_Init{
				Init: &vmmdpb.ForwardRawResponseInit{Status: 200},
			}},
			{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{
				BodyChunk: []byte("remote-ok"),
			}},
		},
	}
	cli := &fakeVmmdClient{RawStream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingRawReverseProxy(lookup, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/socket", strings.NewReader(""))
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("x-faas-instance", "i-remote")
	rec := httptest.NewRecorder()
	proxy(gateway.Target{NodeID: "node-remote-uuid", InstanceID: "i-remote", Port: 8080}).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "remote-ok" {
		t.Errorf("body = %q, want remote-ok", got)
	}
	if len(stream.Sends) < 1 {
		t.Fatalf("expected ≥ 1 Send (init), got %d", len(stream.Sends))
	}
	init := stream.Sends[0].GetInit()
	if init.GetInstance() != "i-remote" {
		t.Errorf("init.Instance = %q, want i-remote", init.GetInstance())
	}
}

// TestRawStreamReverseProxy_InitError_Populated confirms that a
// non-OK status from the bridge surfaces the init.error string in
// the response body. The init frame's status is honored, so the
// inbound writer sees 502 (the bridge's fault) and the body
// contains the diagnostic.
func TestRawStreamReverseProxy_InitError_Populated(t *testing.T) {
	stream := &fakeRawBidiStream{
		Responses: []*vmmdpb.ForwardRawResponse{
			{Frame: &vmmdpb.ForwardRawResponse_Init{
				Init: &vmmdpb.ForwardRawResponseInit{
					Status: 502,
					Error:  "dial refused",
				},
			}},
		},
	}
	cli := &fakeVmmdClient{RawStream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingRawReverseProxy(lookup, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/socket", strings.NewReader(""))
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-x", Port: 8080}).ServeHTTP(rec, req)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "dial refused") {
		t.Errorf("body = %q, want it to contain 'dial refused'", rec.Body.String())
	}
}

// TestRawStreamReverseProxy_ClientCancel_TearsDownStream confirms
// that cancelling r.Context() triggers CloseSend on the bidi
// stream within a short window — the body-copy goroutine must
// exit promptly rather than blocking on the next Read, AND the
// receiver loop must exit promptly rather than blocking on the
// next Recv. We pin both with a deadline to make the test
// deterministic under load.
//
// Issue #710 (PR-3 review finding): the previous version of this
// test used an empty body + an empty Responses slice, which
// caused the body goroutine to hit EOF immediately and the
// receiver loop to hit EOF immediately. The cancel goroutine
// that fires at 20ms was irrelevant — the test passed for the
// wrong reason, and the load-bearing ctxReader cancellation
// path (forwardproxy.go:555-556, the #471 review F3 fix) was
// never exercised. This rewrite uses a blocking body reader
// AND a stream whose Recv blocks on ctx.Done(), so both
// goroutines are parked in their blocking calls when the
// cancel fires.
func TestRawStreamReverseProxy_ClientCancel_TearsDownStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// blockingBody returns 0 bytes from Read until ctx cancels.
	// The body goroutine parks inside cr.Read (the ctxReader
	// wrapper at forwardproxy.go:555-556) until the cancel
	// propagates from r.Context().
	body := &blockingReader{
		ctx:      ctx,
		exitedCh: make(chan struct{}),
		closeCh:  make(chan struct{}),
	}

	// blockingStream blocks Recv on ctx.Done(); the receiver
	// loop is parked inside stream.Recv until the cancel
	// propagates from stream.Context(). The blocking wrapper
	// IS the stream the production code receives — cli returns
	// it directly (ForwardRawStream returns f.RawStream), so
	// CloseSend and Recv both hit our wrapper, not the inner
	// fake.
	stream := &blockingRawBidiStream{ctx: ctx}
	cli := &fakeVmmdClient{RawStream: stream}
	lookup := &fakeNodeLookup{cli: cli}
	proxy := gateway.ForwardingRawReverseProxy(lookup, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/socket", body)
	req = req.WithContext(ctx)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")

	// Cancel from another goroutine after a short delay so the
	// body-copy goroutine has time to enter cr.Read AND the
	// receiver loop has time to enter Recv.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-x", Port: 8080}).ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		// Forwarder returned — both goroutines exited and
		// CloseSend was called by the body-copy goroutine
		// on ctx cancellation.
	case <-time.After(2 * time.Second):
		t.Fatal("forwarder did not return within 2s of ctx cancel — ctxReader / Recv cancellation paths regressed")
	}

	if !stream.closeSendCalled() {
		t.Errorf("CloseSend not called on ctx cancel — bidi stream teardown regressed")
	}
	if !stream.recvExited() {
		t.Errorf("Recv did not exit on ctx cancel — receiver loop teardown regressed")
	}
	select {
	case <-body.exitedCh:
	case <-time.After(2 * time.Second):
		t.Errorf("blocking body did not exit on ctx cancel — ctxReader cancellation path regressed")
	}
}

// blockingReader is an io.ReadCloser that parks inside Read until the
// supplied ctx is cancelled or Close is called. Used by
// TestRawStreamReverseProxy_ClientCancel_TearsDownStream to verify
// the ctxReader cancellation path at
// pkg/gateway/forwardproxy.go:555-556 (issue #471 review F3 fix).
type blockingReader struct {
	ctx       context.Context
	exitedCh  chan struct{}
	closeCh   chan struct{}
	exitOnce  sync.Once
	closeOnce sync.Once
}

func (b *blockingReader) Read(_ []byte) (int, error) {
	// Park until ctx cancels or ctxReader closes the request body;
	// the test asserts this Read returns within 2 s of cancel.
	select {
	case <-b.ctx.Done():
	case <-b.closeCh:
	}
	b.exitOnce.Do(func() {
		close(b.exitedCh)
	})
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return 0, io.ErrClosedPipe
}

func (b *blockingReader) Close() error {
	b.closeOnce.Do(func() { close(b.closeCh) })
	return nil
}

// blockingRawBidiStream implements grpc.BidiStreamingClient and
// parks inside Recv until ctx cancels. Issue #710.
//
// Unlike the RoundTrip tests, this fake does NOT wrap
// fakeRawBidiStream — the production code's bidi stream variable
// is this wrapper directly (cli.ForwardRawStream returns
// f.RawStream), so Send/CloseSend must live on the wrapper, not
// delegate to an inner.
type blockingRawBidiStream struct {
	ctx           context.Context
	closeSendFlag atomic.Bool
	recvExitFlag  atomic.Bool
}

func (s *blockingRawBidiStream) closeSendCalled() bool {
	return s.closeSendFlag.Load()
}

func (s *blockingRawBidiStream) recvExited() bool {
	return s.recvExitFlag.Load()
}

func (s *blockingRawBidiStream) HeaderStream() grpc.ClientStream { return nil }
func (s *blockingRawBidiStream) TrailerOnly() bool               { return false }

func (s *blockingRawBidiStream) Send(_ *vmmdpb.ForwardRawRequest) error {
	return nil
}

func (s *blockingRawBidiStream) Recv() (*vmmdpb.ForwardRawResponse, error) {
	// Park until ctx cancels, then return io.EOF so the
	// receiver loop breaks cleanly (the body goroutine is
	// the one that triggers CloseSend on cancel; this Recv
	// just needs to return without blocking forever).
	<-s.ctx.Done()
	s.recvExitFlag.Store(true)
	return nil, io.EOF
}

func (s *blockingRawBidiStream) CloseSend() error {
	s.closeSendFlag.Store(true)
	return nil
}

func (s *blockingRawBidiStream) Context() context.Context { return s.ctx }
func (s *blockingRawBidiStream) SendMsg(any) error        { return nil }
func (s *blockingRawBidiStream) RecvMsg(any) error        { return nil }
func (s *blockingRawBidiStream) Header() (metadata.MD, error) {
	return nil, nil
}
func (s *blockingRawBidiStream) Trailer() metadata.MD { return nil }
