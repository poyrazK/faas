// End-to-end handler tests via bufconn: an in-process schedd gRPC server backed
// by a fake SchedAPI. Mirrors pkg/vmmdgrpc/bufconn_test.go.

package scheddgrpc_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeEngine struct {
	frameworkReadyFn func(context.Context, string) error
	wakeFn           func(ctx context.Context, appID, deploymentID, scope string) (sched.WakeResult, error)
	admitInstanceFn  func(ctx context.Context, appID, scope string) (sched.WakeResult, error)
	reportFn         func(ctx context.Context, touches []state.InstanceTouch) (int, error)
	parkFn           func(ctx context.Context, instanceID, reason string) error
	// streamLogFn (issue #254 / Move 4) drives the per-frame fan-out
	// in the StreamAppLogs handler tests. Default nil = no-op
	// (returns nil immediately), so the existing test suite stays
	// broken-free.
	streamLogFn func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error
	// streamWarmHintsFn (ADR-025 axis 4) drives the per-event
	// fan-out in the StreamWarmHints handler tests. Default nil =
	// no-op (returns nil immediately).
	streamWarmHintsFn func(ctx context.Context, sink scheddgrpc.WarmHintSink) error
	// capacitySinkFn (ADR-025 axis 5) drives the per-report
	// fan-out in the ReportCapacity handler tests. Default nil =
	// no-op (returns nil immediately, so the server's
	// SendAndClose path is the only completion signal).
	capacitySinkFn func(scheddgrpc.CapacitySink) error
	// destroyFn (issue #554 / ADR-078) drives the
	// ReportLivenessFailed handler tests. Default nil = nil
	// (idempotent no-op, mirroring the production engine's
	// behaviour for a non-RUNNING instance).
	destroyFn func(ctx context.Context, instanceID, reason string) error
	// destroyForWorkloadOOMFn (Cluster C / ADR-121) drives the
	// ReportWorkloadOOM handler tests. Default nil = no-op (mirrors
	// destroyFn above).
	destroyForWorkloadOOMFn func(ctx context.Context, instanceID string, peakMB, planMB int) error
	// forceColdBootFn (P2b of the operator-side observability
	// mega-PR) drives the ForceColdBootNextWake handler tests.
	// Default nil = no-op (returns nil + empty snap IDs).
	forceColdBootFn func(ctx context.Context, deploymentID string) ([]string, error)
	// forceRestartFn (P2d follow-on to PR #1099) drives the
	// ForceRestartInstance handler tests. Default nil = no-op
	// (returns nil + empty snap IDs).
	forceRestartFn func(ctx context.Context, instanceID, reason string) ([]string, error)
}

func (f *fakeEngine) Wake(ctx context.Context, appID, deploymentID, scope, _ string) (sched.WakeResult, error) {
	return f.wakeFn(ctx, appID, deploymentID, scope)
}

func (f *fakeEngine) AdmitInstance(ctx context.Context, appID, deploymentID, scope, _ string) (sched.WakeResult, error) {
	if f.admitInstanceFn != nil {
		return f.admitInstanceFn(ctx, appID, scope)
	}
	// Default: behave like Wake so existing tests that don't set
	// admitInstanceFn continue to compile and pass unchanged.
	// (PR-C widening: deployment_id is forwarded to Wake via "")
	return f.wakeFn(ctx, appID, "", scope)
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) is the sibling
// to AdmitInstance used when the gateway dispatches a mirror
// VM. The bufconn test suite doesn't exercise the mirror hot path,
// so the default delegate mirrors AdmitInstance (Wake-fn or nil).
// Mirror-specific behaviour is asserted by pkg/sched's
// engine_mirror_test.go.
func (f *fakeEngine) AdmitMirrorInstance(ctx context.Context, appID, mirrorDeploymentID, mirrorRuleID string) (sched.WakeResult, error) {
	if f.wakeFn != nil {
		return f.wakeFn(ctx, appID, mirrorDeploymentID, "")
	}
	return sched.WakeResult{}, nil
}

// EnsureWake (ADR-098): the bufconn tests don't exercise single-flight,
// so this delegates to the underlying Wake so legacy tests keep passing.
func (f *fakeEngine) EnsureWake(ctx context.Context, appID, _ string) (sched.CoordOutcome, error) {
	res, err := f.Wake(ctx, appID, "", "", "")
	if err != nil {
		return sched.CoordOutcome{}, err
	}
	return sched.CoordOutcome{
		Instance: &sched.CoordInstance{
			InstanceID: res.InstanceID,
			NodeID:     res.NodeID,
			WakeID:     res.WakeID,
			Port:       int32(res.Port),
			ColdBoot:   res.Method == vmmdpb.WakeMethod_WAKE_COLD_BOOT,
		},
	}, nil
}

func (f *fakeEngine) ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error) {
	return f.reportFn(ctx, touches)
}

func (f *fakeEngine) ParkWithReason(ctx context.Context, instanceID, reason string) error {
	if f.parkFn != nil {
		return f.parkFn(ctx, instanceID, reason)
	}
	return nil
}

// ForceColdBootNextWake (P2b of the operator-side observability
// mega-PR) mirrors ParkWithReason's pattern: default returns nil
// + empty snap IDs; tests that exercise the real RPC behaviour
// inject a forceColdBootFn via the fakeEngine struct.
func (f *fakeEngine) ForceColdBootNextWake(ctx context.Context, deploymentID string) ([]string, error) {
	if f.forceColdBootFn != nil {
		return f.forceColdBootFn(ctx, deploymentID)
	}
	return nil, nil
}

// ForceRestart (P2d follow-on to PR #1099) mirrors
// ForceColdBootNextWake's pattern: default returns nil + empty
// snap IDs; tests that exercise the real RPC behaviour inject
// a forceRestartFn via the fakeEngine struct.
func (f *fakeEngine) ForceRestart(ctx context.Context, instanceID, reason string) ([]string, error) {
	if f.forceRestartFn != nil {
		return f.forceRestartFn(ctx, instanceID, reason)
	}
	return nil, nil
}

// StreamAppLogs (issue #254 / Move 4, issue #517 / PR-B) — the
// default fake drains the sink immediately and returns nil.
// Tests that exercise the per-frame fan-out wire
// (pkg/scheddgrpc/logs_test.go) inject a custom streamLogFn via
// the fakeEngine.
//
// PR-B adds two additive args (sinceWrittenAt, deploymentID); the
// default impl ignores them and the existing test using
// streamLogFn must update its signature to match (see
// pkg/scheddgrpc/logs_test.go::TestStreamAppLogs_HappyPath).
func (f *fakeEngine) StreamAppLogs(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
	if f.streamLogFn != nil {
		return f.streamLogFn(ctx, appID, sinceSeq, sinceWrittenAt, deploymentID, sink)
	}
	return nil
}

// StreamWarmHints (ADR-025 axis 4) — the default fake blocks on
// ctx.Done (the engine's actual StreamWarmHints returns nil on
// ctx cancel even with no emits). Tests that exercise the
// per-event wire (warmhints_test.go) inject a custom
// streamWarmHintsFn that drives events into the sink.
func (f *fakeEngine) StreamWarmHints(ctx context.Context, sink scheddgrpc.WarmHintSink) error {
	if f.streamWarmHintsFn != nil {
		return f.streamWarmHintsFn(ctx, sink)
	}
	<-ctx.Done()
	return nil
}

// CapacitySink (ADR-025 axis 5) — the default fake returns a
// closure that injects a sink (so the handler's Recv loop can
// drive reports into it). Tests that exercise the per-report
// wire (capacity_test.go) inject a custom capacitySinkFn that
// returns the sink they want to receive reports on.
func (f *fakeEngine) CapacitySink() scheddgrpc.CapacitySink {
	if f.capacitySinkFn != nil {
		// capacitySinkFn is the test's hook to drive the
		// handler's sink externally; we surface a sink that
		// simply no-ops so the handler's table.Replace call
		// is harmless.
		_ = f.capacitySinkFn
	}
	return func(sched.CapacityReport) error { return nil }
}

// NodeKeyRegistry (ADR-053 slice 3) — the default fake returns
// nil to disable signature verification (pre-slice-3 mode).
// Tests that exercise the strict-mode path inject a populated
// registry via a wrapper (see capacity_test.go).
func (f *fakeEngine) NodeKeyRegistry() *sched.NodeKeyRegistry { return nil }

// DestroyForLivenessFailure (issue #554 / ADR-078) — the default
// fake returns nil (idempotent no-op, mirroring the production
// engine's behaviour for a non-RUNNING instance). Tests that
// exercise the failure classification wire
// (liveness_failed_test.go) inject a custom destroyFn.
func (f *fakeEngine) DestroyForLivenessFailure(ctx context.Context, instanceID, reason string) error {
	if f.destroyFn != nil {
		return f.destroyFn(ctx, instanceID, reason)
	}
	return nil
}

// DestroyForWorkloadOOMFailure (Cluster C / ADR-121) — the default
// fake returns nil. Tests that exercise the failure classification
// wire (workload_oom_test.go) inject a custom
// destroyForWorkloadOOMFn.
func (f *fakeEngine) DestroyForWorkloadOOMFailure(ctx context.Context, instanceID string, peakMB, planMB int) error {
	if f.destroyForWorkloadOOMFn != nil {
		return f.destroyForWorkloadOOMFn(ctx, instanceID, peakMB, planMB)
	}
	return nil
}

func newServer(t *testing.T, eng scheddgrpc.SchedAPI) scheddpb.ScheddClient {
	t.Helper()
	srv := grpc.NewServer()
	scheddgrpc.New(eng, wire.NewOpsMetrics("schedd_test"), nil).Register(srv)

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
	t.Cleanup(func() { _ = conn.Close() })
	return scheddpb.NewScheddClient(conn)
}

// newServerWithMetrics is the filter-counter sibling to
// newServer (issue #309 / tier-2 DX). The drop-counter
// whitebox tests (TestStreamAppLogs_FilterLevelDropsAndCounts
// and friends) need to scrape the per-reason
// apid_logs_dropped_total counter off the same *wire.OpsMetrics
// the schedd server increments — the helper returns both. Same
// prefix ("schedd_test") as newServer so the counter series
// surface in /metrics with the same names.
func newServerWithMetrics(t *testing.T, eng scheddgrpc.SchedAPI) (scheddpb.ScheddClient, *wire.OpsMetrics) {
	t.Helper()
	metrics := wire.NewOpsMetrics("schedd_test")
	srv := grpc.NewServer()
	scheddgrpc.New(eng, metrics, nil).Register(srv)

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
	t.Cleanup(func() { _ = conn.Close() })
	return scheddpb.NewScheddClient(conn), metrics
}

// readCounter scrapes the per-reason <prefix>_logs_dropped_total
// counter for the given reason label. Used by the filter-counter
// whitebox tests (TestStreamAppLogs_FilterLevelDropsAndCounts /
// FilterGrepDropsAndCounts / the combined level+grep tiebreaker
// test) to verify the schedd sink increments
// apid_logs_dropped_total{reason} on each drop.
//
// Implementation note: we read the typed Counter via
// OpsMetrics.LogsDropped(reason) and prometheus/testutil.ToFloat64
// instead of walking the registry's Gather() output. Gather()
// traverses every registered family under a registry mutex and
// on cold CI runners occasionally returns a stale snapshot when
// the increment and the read are in flight on different
// goroutines — that was the source of the
// `schedd_test_logs_dropped_total{reason="filter_level"} = 1, want 2`
// flake on TestStreamAppLogs_FilterLevelDropsAndCounts. Reading
// the counter directly goes through the counter's internal atomic
// load and removes the registry-mutex critical section from the
// read side. The metricName parameter is retained so callers stay
// diff-clean; it must equal "<prefix>_logs_dropped_total".
func readCounter(t *testing.T, m *wire.OpsMetrics, metricName string, reason string) float64 {
	t.Helper()
	counter := m.LogsDropped(reason)
	if counter == nil {
		t.Fatalf("metric %s{reason=%q} not exposed by OpsMetrics (closed-set check failed)", metricName, reason)
	}
	return testutil.ToFloat64(counter)
}

func TestWake_Success(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{InstanceID: "i-1", NodeID: "node-test-1", Method: vmmdpb.WakeMethod_WAKE_RESTORE}, nil
		},
	})
	resp, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if resp.GetNodeId() != "node-test-1" {
		t.Errorf("node_id = %q", resp.GetNodeId())
	}
	if resp.GetMethod() != scheddpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("method = %v, want WAKE_RESTORE", resp.GetMethod())
	}
}

func TestWake_CapacityDenialSurfacesProblem(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, api.ErrCapacity("no RAM headroom")
		},
	})
	_, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err == nil {
		t.Fatal("expected capacity denial")
	}
	// api.CodeCapacity maps to ResourceExhausted (grpcerr) so the gateway serves 503.
	if code := status.Code(err); code != codes.ResourceExhausted {
		t.Errorf("code = %v, want ResourceExhausted", code)
	}
}

func TestWake_PlainErrorIsInternal(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, errors.New("db exploded")
		},
	})
	_, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("code = %v, want Internal", code)
	}
}

func TestReportActivity(t *testing.T) {
	var got []state.InstanceTouch
	cli := newServer(t, &fakeEngine{
		reportFn: func(_ context.Context, touches []state.InstanceTouch) (int, error) {
			got = touches
			return len(touches), nil
		},
	})
	now := time.Now().UnixMilli()
	resp, err := cli.ReportActivity(context.Background(), &scheddpb.ReportActivityRequest{
		Touches: []*scheddpb.Touch{
			{InstanceId: "i-1", UnixMs: now},
			{InstanceId: "i-2", UnixMs: now},
		},
	})
	if err != nil {
		t.Fatalf("ReportActivity: %v", err)
	}
	if resp.GetApplied() != 2 {
		t.Errorf("applied = %d, want 2", resp.GetApplied())
	}
	if len(got) != 2 || got[0].InstanceID != "i-1" {
		t.Errorf("touches = %+v", got)
	}
	if got[0].LastRequest.UnixMilli() != now {
		t.Errorf("touch time round-trip lost: %v", got[0].LastRequest)
	}
}

// TestWake_PropagatesWakeID asserts the per-wake stable identifier
// minted by schedd's engine reaches the gRPC response verbatim. The
// gatewayd-internal client reads resp.GetWakeId() and sets it as the
// x-faas-wake-id response header — if this contract breaks, downstream
// logs and dashboards lose their correlation key.
func TestWake_PropagatesWakeID(t *testing.T) {
	const wantWakeID = "0193f7c0-1234-7abc-9def-0123456789ab"
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_RESTORE,
				WakeID:     wantWakeID,
			}, nil
		},
	})
	resp, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if got := resp.GetWakeId(); got != wantWakeID {
		t.Errorf("wake_id = %q, want %q", got, wantWakeID)
	}
}

// TestAdmitInstance_AdmitsNewInstance (issue #168) — the happy path:
// the engine returns a WakeResult with identity fields populated and
// AtCapacity=false. The wire must reflect exactly that.
func TestAdmitInstance_AdmitsNewInstance(t *testing.T) {
	const wantWakeID = "0193f7c0-aaaa-7abc-9def-0123456789ab"
	cli := newServer(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_COLD_BOOT,
				WakeID:     wantWakeID,
			}, nil
		},
	})
	resp, err := cli.AdmitInstance(context.Background(), &scheddpb.AdmitInstanceRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("AdmitInstance: %v", err)
	}
	if got := resp.GetInstanceId(); got != "i-1" {
		t.Errorf("instance_id = %q, want i-1", got)
	}
	if got := resp.GetNodeId(); got != "node-test-1" {
		t.Errorf("node_id = %q, want node-test-1", got)
	}
	if resp.GetMethod() != scheddpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("method = %v, want WAKE_COLD_BOOT", resp.GetMethod())
	}
	if got := resp.GetWakeId(); got != wantWakeID {
		t.Errorf("wake_id = %q, want %q", got, wantWakeID)
	}
	if resp.GetAtCapacity() {
		t.Errorf("at_capacity = true on admit path; want false")
	}
}

// TestAdmitInstance_AtCapacityIsTypedResult (issue #168) — the benign
// "already at max_concurrency" outcome must surface as at_capacity=true,
// identity fields empty, no gRPC error. The gateway treats this as a
// no-op when it already has ≥1 cached target.
func TestAdmitInstance_AtCapacityIsTypedResult(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{AtCapacity: true}, nil
		},
	})
	resp, err := cli.AdmitInstance(context.Background(), &scheddpb.AdmitInstanceRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("AdmitInstance: %v (at_capacity must NOT be lifted to an error)", err)
	}
	if !resp.GetAtCapacity() {
		t.Errorf("at_capacity = false; want true")
	}
	if got := resp.GetInstanceId(); got != "" {
		t.Errorf("instance_id = %q on at_capacity path; want empty", got)
	}
	if got := resp.GetNodeId(); got != "" {
		t.Errorf("node_id = %q on at_capacity path; want empty", got)
	}
	if got := resp.GetWakeId(); got != "" {
		t.Errorf("wake_id = %q on at_capacity path; want empty", got)
	}
}

// TestAdmitInstance_RealFailureSurfacesProblem (issue #168) — only
// genuine admission failures (RAM headroom, store error) travel as
// gRPC errors with the RFC 7807 problem. At-capacity must not.
func TestAdmitInstance_RealFailureSurfacesProblem(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{}, api.ErrCapacity("RAM headroom exhausted")
		},
	})
	_, err := cli.AdmitInstance(context.Background(), &scheddpb.AdmitInstanceRequest{AppId: "app-1"})
	if err == nil {
		t.Fatalf("AdmitInstance: want error on capacity; got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status-shaped error, got %T", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("code = %v; want ResourceExhausted", st.Code())
	}
	if p, ok := grpcerr.FromStatus(err); !ok || p == nil || p.Code != api.CodeCapacity {
		t.Errorf("problem = %v; want CodeCapacity", p)
	}
}

// TestServerParkInstance_NotFoundReturnsNotFoundStatus covers the
// `errors.Is(err, state.ErrNotFound)` branch of Server.ParkInstance
// (server.go:152-156). When the engine says "row was already
// gone", the server must surface that as a gRPC NotFound status
// so the meterd quota loop can match it via codes.NotFound and
// decide to log-and-continue rather than treat it as a hard
// failure.
func TestServerParkInstance_NotFoundReturnsNotFoundStatus(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		parkFn: func(context.Context, string, string) error {
			return state.ErrNotFound
		},
	})
	_, err := cli.ParkInstance(context.Background(), &scheddpb.ParkInstanceRequest{
		InstanceId: "i-gone",
		Reason:     "idle",
	})
	if err == nil {
		t.Fatal("expected NotFound status, got nil")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want NotFound", code)
	}
}

// TestServerParkInstance_InternalError covers the catch-all
// branch of Server.ParkInstance (server.go:157) for any non-
// NotFound error. The wire shape is the same Internal status the
// other RPCs produce; the difference is that the message text
// is the engine's error string (not the api.Problem shape Wake
// and AdmitInstance use). This documents the boundary for
// meterd's error handling.
func TestServerParkInstance_InternalError(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		parkFn: func(context.Context, string, string) error {
			return errors.New("store exploded")
		},
	})
	_, err := cli.ParkInstance(context.Background(), &scheddpb.ParkInstanceRequest{
		InstanceId: "i-1",
		Reason:     "idle",
	})
	if err == nil {
		t.Fatal("expected Internal status, got nil")
	}
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("code = %v, want Internal", code)
	}
}

// TestWake_PropagatesPort pins issue #460 / ADR-053 (PR-C): the
// per-deployment override port schedd computes during admit must
// reach the WakeResponse wire frame so the gateway's
// Client.AdmitInstance wrapper can hand it to the upstream forwarder.
// A regression that drops Port from the response forces the gateway
// to dial :8080 against a guest that bound :9090 → 503.
func TestWake_PropagatesPort(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		wakeFn: func(context.Context, string, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_RESTORE,
				Port:       9090,
			}, nil
		},
	})
	resp, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if got := resp.GetPort(); got != 9090 {
		t.Errorf("port = %d, want 9090", got)
	}
}

// TestAdmitInstance_PropagatesPort mirrors the Wake port test for
// the AdmitInstance RPC, which is the production-path callgatewayd-internal
// actually uses. The legacy Wake call still carries Port for
// back-compat with any pre-sliced-4 callers, but the gateway forward
// path is on AdmitInstance + Target.Port in pkg/gateway.
func TestAdmitInstance_PropagatesPort(t *testing.T) {
	cli := newServer(t, &fakeEngine{
		admitInstanceFn: func(context.Context, string, string) (sched.WakeResult, error) {
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_COLD_BOOT,
				Port:       9090,
			}, nil
		},
	})
	resp, err := cli.AdmitInstance(context.Background(), &scheddpb.AdmitInstanceRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("AdmitInstance: %v", err)
	}
	if got := resp.GetPort(); got != 9090 {
		t.Errorf("port = %d, want 9090", got)
	}
}

// TestWake_DeploymentIDForwarded (issue #556 / PR-C) pins the
// wire-contract widening: WakeRequest.deployment_id reaches
// SchedAPI.Wake as the second arg; the engine's response
// deployment_id surfaces in WakeResponse.deployment_id (field 7).
// This is the only mechanism by which the gateway can route a
// cold-bucket wake onto a specific live deployment — without it
// the wake-fan-out path in pkg/gateway/handler.go would have no
// way to disambiguate sibling live deployments.
func TestWake_DeploymentIDForwarded(t *testing.T) {
	var gotDeploymentID string
	cli := newServer(t, &fakeEngine{
		wakeFn: func(_ context.Context, appID, deploymentID, scope string) (sched.WakeResult, error) {
			gotDeploymentID = deploymentID
			return sched.WakeResult{
				InstanceID:   "i-1",
				NodeID:       "node-test-1",
				Method:       vmmdpb.WakeMethod_WAKE_RESTORE,
				DeploymentID: deploymentID,
			}, nil
		},
	})
	resp, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{
		AppId:        "app-1",
		DeploymentId: "dep-canary",
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if gotDeploymentID != "dep-canary" {
		t.Errorf("engine received deploymentID = %q, want %q", gotDeploymentID, "dep-canary")
	}
	if got := resp.GetDeploymentId(); got != "dep-canary" {
		t.Errorf("response deployment_id = %q, want %q", got, "dep-canary")
	}
}

// TestWake_DeploymentIDEmptyPreserved (issue #556 / PR-C) pins the
// legacy-compat path: when WakeRequest.deployment_id is empty the
// engine receives "" and the response carries "" — the field is
// additive per ADR-016 and an empty value MUST NOT be silently
// rewritten to the newest live deployment (that decision lives in
// the engine, not in the wire layer).
func TestWake_DeploymentIDEmptyPreserved(t *testing.T) {
	var gotDeploymentID string = "sentinel"
	cli := newServer(t, &fakeEngine{
		wakeFn: func(_ context.Context, appID, deploymentID, scope string) (sched.WakeResult, error) {
			gotDeploymentID = deploymentID
			return sched.WakeResult{
				InstanceID: "i-1",
				NodeID:     "node-test-1",
				Method:     vmmdpb.WakeMethod_WAKE_RESTORE,
			}, nil
		},
	})
	resp, err := cli.Wake(context.Background(), &scheddpb.WakeRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if gotDeploymentID != "" {
		t.Errorf("engine received deploymentID = %q, want \"\" (legacy mode)", gotDeploymentID)
	}
	if got := resp.GetDeploymentId(); got != "" {
		t.Errorf("response deployment_id = %q, want \"\" (legacy mode)", got)
	}
}

func (f *fakeEngine) ReportFrameworkReady(ctx context.Context, id string) error {
	if f.frameworkReadyFn != nil {
		return f.frameworkReadyFn(ctx, id)
	}
	return nil
}
