package scheddgrpc_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// newClient stands up an in-process schedd server backed by eng and returns a
// scheddgrpc.Client dialed to it (the same wrapper gatewayd-internal uses).
func newClient(t *testing.T, eng scheddgrpc.SchedAPI) *scheddgrpc.Client {
	t.Helper()
	srv := grpc.NewServer()
	scheddgrpc.New(eng, wire.NewOpsMetrics("schedd_client_test"), nil).Register(srv)

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := scheddgrpc.NewClient(conn)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientWake_ReturnsNodeID(t *testing.T) {
	c := newClient(t, &fakeEngine{
		wakeFn: func(_ context.Context, appID, _, _ string) (sched.WakeResult, error) {
			if appID != "app-1" {
				t.Errorf("appID = %q", appID)
			}
			return sched.WakeResult{InstanceID: "i-1", NodeID: "node-test-1", Method: vmmdpb.WakeMethod_WAKE_RESTORE}, nil
		},
	})
	instanceID, nodeID, _, wakeID, _, err := c.Wake(context.Background(), "app-1", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if nodeID != "node-test-1" {
		t.Errorf("nodeID = %q", nodeID)
	}
	if instanceID != "i-1" {
		t.Errorf("instanceID = %q, want i-1", instanceID)
	}
	if wakeID == "" {
		// Phase 1 fast path returns empty wake_id (the existing
		// RUNNING instance was minted by an earlier wake). The fake
		// engine returns WAKE_RESTORE which is a fast-path return,
		// so wake_id stays unset here. The dedicated wake_id
		// propagation is covered by TestClientWake_PropagatesWakeID.
		t.Logf("wakeID empty on fast-path return (expected); no assertion")
	}
}

func TestClientWake_CapacityLiftsToProblem(t *testing.T) {
	c := newClient(t, &fakeEngine{
		wakeFn: func(context.Context, string, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, api.ErrCapacity("no RAM headroom")
		},
	})
	_, _, _, _, _, err := c.Wake(context.Background(), "app-1", "", "")
	if err == nil {
		t.Fatal("expected capacity denial")
	}
	// The wire status must lift back to *api.Problem so the gateway maps it to
	// the right RFC 7807 response (503) without re-classifying strings.
	prob := mustProblem(t, api.AsProblem(err))
	if prob.Status != 503 {
		t.Errorf("problem status = %d, want 503", prob.Status)
	}
}

// TestClientWake_PropagatesPort pins issue #460 / ADR-053 (PR-C): the
// per-deployment override port the engine computed must surface in
// Client.Wake's return tuple so gatewayd-internal callers can stamp it onto
// ForwardHTTPRequestInit. The fake stub here mirrors the engine
// contract: WakeResult.Port populated → Client.Wake's 5-tuple last
// value matches.
func TestClientWake_PropagatesPort(t *testing.T) {
	c := newClient(t, &fakeEngine{
		wakeFn: func(context.Context, string, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_RESTORE,
				Port:       9090,
			}, nil
		},
	})
	_, _, _, _, port, err := c.Wake(context.Background(), "app-1", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if port != 9090 {
		t.Errorf("port = %d, want 9090", port)
	}
}

func TestClientReportActivity(t *testing.T) {
	var got []state.InstanceTouch
	c := newClient(t, &fakeEngine{
		reportFn: func(_ context.Context, touches []state.InstanceTouch) (int, error) {
			got = touches
			return len(touches), nil
		},
	})
	now := time.UnixMilli(1_700_000_000_000)
	applied, err := c.ReportActivity(context.Background(), []state.InstanceTouch{
		{InstanceID: "i-1", LastRequest: now},
		{InstanceID: "i-2", LastRequest: now},
	})
	if err != nil {
		t.Fatalf("ReportActivity: %v", err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}
	if len(got) != 2 || got[0].InstanceID != "i-1" || !got[0].LastRequest.Equal(now) {
		t.Errorf("touches round-trip = %+v", got)
	}
}

func TestDial_EmptyPath(t *testing.T) {
	if _, err := scheddgrpc.Dial(""); err == nil {
		t.Fatal("expected error on empty socket path")
	}
}

func TestClient_CloseNilConn(t *testing.T) {
	var c scheddgrpc.Client
	if err := c.Close(); err != nil {
		t.Errorf("Close on zero client = %v, want nil", err)
	}
}

// TestClientAdmitInstance_AdmitsNewInstance (issue #168) — the
// happy path on the high-level Client wrapper: the engine returns
// a WakeResult with identity fields populated and AtCapacity=false,
// and the wrapper surfaces all four return values to the caller.
// The bufconn proto-level coverage in bufconn_test.go proves the
// wire shape; this test proves the wrapper's translation.
func TestClientAdmitInstance_AdmitsNewInstance(t *testing.T) {
	const wantWakeID = "0193f7c0-bbbb-7abc-9def-0123456789ab"
	c := newClient(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "n-1",
				Method:     vmmdpb.WakeMethod_WAKE_COLD_BOOT,
				WakeID:     wantWakeID,
			}, nil
		},
	})
	instanceID, nodeID, _, wakeID, method, atCapacity, _, err := c.AdmitInstance(context.Background(), "app-1", "", "", "")
	if err != nil {
		t.Fatalf("AdmitInstance: %v", err)
	}
	if instanceID != "i-1" {
		t.Errorf("instanceID = %q, want i-1", instanceID)
	}
	if nodeID != "n-1" {
		t.Errorf("nodeID = %q, want n-1", nodeID)
	}
	if wakeID != wantWakeID {
		t.Errorf("wakeID = %q, want %q", wakeID, wantWakeID)
	}
	if atCapacity {
		t.Errorf("atCapacity = true on admit path; want false")
	}
	// PR scale-out readiness: the wire's WAKE_COLD_BOOT (proto value 0)
	// must pass through AdmitInstance as int32 0.
	if method != 0 {
		t.Errorf("method = %d, want 0 (WAKE_COLD_BOOT)", method)
	}
}

type burstContinuationEngine struct {
	fakeEngine
	mu              sync.Mutex
	flags           []bool
	placementSpread []bool
}

func (e *burstContinuationEngine) AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (sched.WakeResult, error) {
	e.mu.Lock()
	e.flags = append(e.flags, sched.IsBurstContinuation(ctx))
	e.placementSpread = append(e.placementSpread, sched.IsBurstPlacementSpread(ctx))
	e.mu.Unlock()
	return e.fakeEngine.AdmitInstance(ctx, appID, deploymentID, scope, trigger)
}

func TestClientAdmitInstancesCarriesContinuationMarker(t *testing.T) {
	eng := &burstContinuationEngine{
		fakeEngine: fakeEngine{
			admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
				return sched.WakeResult{InstanceID: "i-1", NodeID: "node-1", WakeID: "wake-1"}, nil
			},
		},
	}
	c := newClient(t, eng)

	var mu sync.Mutex
	results := 0
	err := c.AdmitInstances(context.Background(), "app-1", "", sched.TriggerGateway, 4,
		func(instanceID, nodeID, deploymentID, wakeID string, method int32, atCapacity bool, port int, err error) {
			if err != nil {
				t.Errorf("reported admission error: %v", err)
			}
			mu.Lock()
			results++
			mu.Unlock()
		})
	if err != nil {
		t.Fatalf("AdmitInstances: %v", err)
	}
	if results != 4 {
		t.Fatalf("reported results = %d, want 4", results)
	}
	eng.mu.Lock()
	defer eng.mu.Unlock()
	if len(eng.flags) != 4 {
		t.Fatalf("engine calls = %d, want 4", len(eng.flags))
	}
	continuations := 0
	for _, continuation := range eng.flags {
		if continuation {
			continuations++
		}
	}
	if continuations != 3 {
		t.Fatalf("continuation calls = %d, want 3", continuations)
	}
	if len(eng.placementSpread) != 4 {
		t.Fatalf("placement marker calls = %d, want 4", len(eng.placementSpread))
	}
	if eng.placementSpread[0] {
		t.Error("first admission unexpectedly carried placement-spread marker")
	}
	for i, spread := range eng.placementSpread[1:] {
		if !spread {
			t.Errorf("continuation #%d missing placement-spread marker", i+1)
		}
	}
}

// TestClientAdmitInstance_AtCapacityIsTypedResult (issue #168) —
// the benign "already at max_concurrency" outcome must surface as
// atCapacity=true with empty identity fields and no error. The
// gateway treats this as a no-op when it already has ≥1 cached
// target; an error here would be a 503 instead of a 200 with
// at-capacity metadata.
func TestClientAdmitInstance_AtCapacityIsTypedResult(t *testing.T) {
	c := newClient(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{AtCapacity: true}, nil
		},
	})
	instanceID, nodeID, _, wakeID, method, atCapacity, _, err := c.AdmitInstance(context.Background(), "app-1", "", "", "")
	if err != nil {
		t.Fatalf("AdmitInstance: at_capacity must NOT be lifted to an error; got %v", err)
	}
	if !atCapacity {
		t.Errorf("atCapacity = false; want true")
	}
	if instanceID != "" || nodeID != "" || wakeID != "" {
		t.Errorf("identity fields populated on at_capacity path: i=%q n=%q w=%q",
			instanceID, nodeID, wakeID)
	}
	// PR scale-out readiness: at_capacity path leaves method at 0
	// (unset) so the gateway's scheddWakeMethodToGateway default-branch
	// maps it to WakeMethodColdBoot — the gateway does NOT enumerate
	// the locality classifier on this path.
	if method != 0 {
		t.Errorf("method = %d on at_capacity path, want 0 (unset)", method)
	}
}

// TestClientAdmitInstance_LiftsError covers the liftErr path on
// AdmitInstance: a real admission failure (RAM headroom, etc.)
// must surface as an *api.Problem the gateway can route to 503.
// The bufconn test already covers the wire translation; this
// test covers the client-side unwrap.
func TestClientAdmitInstance_LiftsError(t *testing.T) {
	c := newClient(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, api.ErrCapacity("no RAM headroom")
		},
	})
	_, _, _, _, _, _, _, err := c.AdmitInstance(context.Background(), "app-1", "", "", "")
	if err == nil {
		t.Fatal("expected capacity denial on AdmitInstance")
	}
	prob := mustProblem(t, api.AsProblem(err))
	if prob.Status != 503 {
		t.Errorf("problem status = %d, want 503", prob.Status)
	}
}

// TestClientAdmitInstance_PropagatesPort pins issue #460 / ADR-053
// (PR-C) on the production path: Client.AdmitInstance is what the
// gateway actually calls, and its 7-tuple return carries the
// override port as the last value. A regression that drops Port
// would silently force the gateway to dial :8080 against any
// deployment that set --port.
func TestClientAdmitInstance_PropagatesPort(t *testing.T) {
	c := newClient(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_COLD_BOOT,
				Port:       9090,
			}, nil
		},
	})
	_, _, _, _, _, _, port, err := c.AdmitInstance(context.Background(), "app-1", "", "", "")
	if err != nil {
		t.Fatalf("AdmitInstance: %v", err)
	}
	if port != 9090 {
		t.Errorf("port = %d, want 9090", port)
	}
}

// TestClientParkInstance_Ok covers the happy path: the engine
// parks the instance, the wrapper returns nil. Most of meterd's
// quota loop depends on this returning nil so a successful park
// doesn't log a spurious error.
func TestClientParkInstance_Ok(t *testing.T) {
	c := newClient(t, &fakeEngine{
		parkFn: func(_ context.Context, instanceID, reason string) error {
			if instanceID != "i-1" || reason != "idle" {
				t.Errorf("park args = (%q, %q)", instanceID, reason)
			}
			return nil
		},
	})
	if err := c.ParkInstance(context.Background(), "i-1", "idle", ""); err != nil {
		t.Errorf("ParkInstance: %v", err)
	}
}

// TestClientParkInstance_NotFound documents the boundary: when
// the engine returns state.ErrNotFound (the instance was already
// gone before we got there), the wrapper must surface it as the
// typed sentinel so meterd's errors.Is check works without string
// matching. Anything else (a generic error, a gRPC status) and the
// quota loop logs noise on every idle eviction.
func TestClientParkInstance_NotFound(t *testing.T) {
	c := newClient(t, &fakeEngine{
		parkFn: func(context.Context, string, string) error {
			return state.ErrNotFound
		},
	})
	err := c.ParkInstance(context.Background(), "i-1", "idle", "")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ParkInstance = %v, want errors.Is(state.ErrNotFound)", err)
	}
}

// TestClientParkInstance_PlainErrorPassesThrough documents the
// other side of the same boundary: a non-NotFound error from the
// engine is wrapped by the Server into a gRPC status with code
// Internal (server.go:157: `status.Error(codes.Internal, err.Error())`)
// and the resulting wire status is what the Client surfaces back.
// The meterd quota loop distinguishes this from the NotFound case
// by checking the gRPC code, not by string matching the error.
//
// Note: it is the Server, not the Client, that does the wrapping.
// The Client's liftErr on a status whose `details` carry no
// api.Problem returns the status unchanged (liftErr only unwraps
// the specific NotFound case via `errors.Is(err, state.ErrNotFound)`
// in client.go:142-146). A future refactor that lifts ALL errors
// to *api.Problem at the Client layer would break this test and
// the meterd boundary it pins.
func TestClientParkInstance_PlainErrorPassesThrough(t *testing.T) {
	c := newClient(t, &fakeEngine{
		parkFn: func(context.Context, string, string) error {
			return errors.New("db boom")
		},
	})
	got := c.ParkInstance(context.Background(), "i-1", "idle", "")
	if got == nil {
		t.Fatal("expected error from ParkInstance")
	}
	if errors.Is(got, state.ErrNotFound) {
		t.Errorf("plain error lifted to state.ErrNotFound; that path is NotFound-only")
	}
	// Wire shape: gRPC status with code Internal. The message text
	// includes the engine's error string (server.go:157 wraps with
	// status.Error(codes.Internal, err.Error())).
	if code := status.Code(got); code != codes.Internal {
		t.Errorf("code = %v, want Internal", code)
	}
}

// --- Issue #254 / Move 4 — pkg/scheddgrpc.Client.StreamAppLogs ---
//
// These cases pin the transport-neutral conversion in client_logs.go:
// every StreamAppLogsResponse field round-trips into a LogFrame field,
// io.EOF flows back as exactly io.EOF (no lifting), and gRPC status
// errors pass through untouched so the apid SSE handler's
// renderAppLogsError can map codes directly. They use the bufconn
// newClient helper from the top of this file.

// TestClientStreamAppLogs_FrameConversion asserts that every field
// of the upstream scheddpb.StreamAppLogsResponse reaches the typed
// LogFrame 1:1, including time.Time nanosecond equality. The schedd
// fan-out populates all five fields per frame (issue #254 acceptance
// #5); this is the contract cmd/apid's writeAppLogEvent depends on.
func TestClientStreamAppLogs_FrameConversion(t *testing.T) {
	const (
		wantAppID = "app-1"
		wantSeq   = int64(42)
		wantLine  = "hello world\n"
	)
	wantWrittenAt := time.Date(2026, 7, 29, 12, 0, 0, 123_000_000, time.UTC)
	var gotAppID string
	var gotSinceSeq int64

	c := newClient(t, &fakeEngine{
		streamLogFn: func(_ context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			gotAppID = appID
			gotSinceSeq = sinceSeq
			return sink(sched.LogFrame{InstanceID: "i-99", Seq: wantSeq, Stream: "stdout", Line: wantLine, WrittenAt: wantWrittenAt})
		},
	})

	stream, err := c.StreamAppLogs(context.Background(), wantAppID, 7, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	t.Cleanup(func() { _ = stream }) // typed interface — no Close

	f, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if f.InstanceID != "i-99" {
		t.Errorf("InstanceID = %q, want i-99", f.InstanceID)
	}
	if f.Seq != wantSeq {
		t.Errorf("Seq = %d, want %d", f.Seq, wantSeq)
	}
	if f.Stream != "stdout" {
		t.Errorf("Stream = %q, want stdout", f.Stream)
	}
	if f.Line != wantLine {
		t.Errorf("Line = %q, want %q", f.Line, wantLine)
	}
	if !f.WrittenAt.Equal(wantWrittenAt) {
		t.Errorf("WrittenAt = %v, want %v", f.WrittenAt, wantWrittenAt)
	}
	if gotAppID != wantAppID {
		t.Errorf("engine appID = %q, want %q", gotAppID, wantAppID)
	}
	if gotSinceSeq != 7 {
		t.Errorf("engine sinceSeq = %d, want 7", gotSinceSeq)
	}
}

// TestClientStreamAppLogs_CleanEOFReturnsExactEOF asserts that the
// adapter propagates io.EOF verbatim, not wrapped (no fmt.Errorf
// "EOF" decorations that would break errors.Is in the apid loop).
// The recvCh close + error path in serveAppLogs hinges on this
// branch (handlers_ext.go).
func TestClientStreamAppLogs_CleanEOFReturnsExactEOF(t *testing.T) {
	c := newClient(t, &fakeEngine{
		streamLogFn: func(_ context.Context, _ string, _ int64, _ time.Time, _ string, _ scheddgrpc.LogFrameSink) error {
			return nil
		},
	})
	stream, err := c.StreamAppLogs(context.Background(), "app-1", 0, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	_, err = stream.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Recv err = %v, want io.EOF", err)
	}
}

// TestClientStreamAppLogs_NotFoundPassesThrough asserts the
// transport-neutral contract: state.ErrNotFound from the engine
// surfaces as a raw gRPC NotFound status on Recv, NOT as an
// *api.Problem. liftErr is not applied because the SSE handler
// needs the raw code (cmd/apid/handlers_ext.go::renderAppLogsError).
func TestClientStreamAppLogs_NotFoundPassesThrough(t *testing.T) {
	c := newClient(t, &fakeEngine{
		streamLogFn: func(_ context.Context, _ string, _ int64, _ time.Time, _ string, _ scheddgrpc.LogFrameSink) error {
			return state.ErrNotFound
		},
	})
	stream, err := c.StreamAppLogs(context.Background(), "app-1", 0, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("Recv: want error, got nil")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want NotFound", code)
	}
}

// TestClientStreamAppLogs_GenericErrorIsUnavailable asserts the
// second branch of server.go:289-297: anything that isn't
// ErrNotFound / context-cancel surfaces as codes.Unavailable so the
// apid SSE handler's `event: degraded {reason: schedd_unreachable}`
// envelope fires.
func TestClientStreamAppLogs_GenericErrorIsUnavailable(t *testing.T) {
	c := newClient(t, &fakeEngine{
		streamLogFn: func(_ context.Context, _ string, _ int64, _ time.Time, _ string, _ scheddgrpc.LogFrameSink) error {
			return errors.New("vmmd dial failed")
		},
	})
	stream, err := c.StreamAppLogs(context.Background(), "app-1", 0, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	_, err = stream.Recv()
	if code := status.Code(err); code != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable", code)
	}
}

// TestClientStreamAppLogs_ContextCancelUnblocks asserts that
// cancelling the caller's context unblocks a blocked Recv without
// a hang. The receive goroutine in cmd/apid/handlers_ext.go relies
// on streamCtx cancellation to exit; without this guarantee the
// apid would leak a goroutine on every closed SSE.
func TestClientStreamAppLogs_ContextCancelUnblocks(t *testing.T) {
	release := make(chan struct{})
	c := newClient(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, _ string, _ int64, _ time.Time, _ string, sink scheddgrpc.LogFrameSink) error {
			// Block until the caller cancels — emulate an idle
			// vmmd stream that hasn't seen new bytes.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := c.StreamAppLogs(ctx, "app-1", 0, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := stream.Recv(); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Recv returned nil after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv did not unblock after cancel; goroutine leak")
	}
	close(release)
}

// mustProblem fails the test when prob is nil and returns it
// non-nil otherwise. Side-steps the SA5011 false positive on
// `if prob == nil { t.Fatalf(...) } if prob.X` because the
// linter cannot prove t.Fatalf never returns.
func mustProblem(t *testing.T, prob *api.Problem) *api.Problem {
	t.Helper()
	if prob == nil {
		t.Fatal("expected a problem, got nil")
	}
	return prob
}
