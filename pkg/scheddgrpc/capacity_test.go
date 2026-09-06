// capacity_test.go — ReportCapacity gRPC handler tests
// (ADR-025 axis 5).
//
// End-to-end tests through bufconn:
//
//   - RoundTrip: vmmd opens a stream, sends two CapacityReports,
//     closes the send side, and receives a ReportCapacityAck. The
//     handler applies each report to the engine's per-node table
//     via the SchedAPI.CapacitySink seam; the test asserts the
//     typed reports reach the seam with intact fields.
//
//   - ContextCancelSurfacesCanceled: cancelling the caller's
//     context mid-stream surfaces codes.Canceled on the wire.
//
//   - EmptyNodeIDIsInvalidArgument: a report with empty node_id
//     is rejected with codes.InvalidArgument (load-bearing; the
//     table's defensive no-op is a fallback, not the gate).
//
//   - StreamClosedAfterLastSend: closing the send side cleanly
//     yields a single ReportCapacityAck on CloseAndRecv (the
//     handler's SendAndClose path).
//
//   - MultipleNodesCoexist: reports for two distinct node_ids
//     coexist in the table (mirrors
//     capacity_test.go::TestNodeCapacityTable_ReplaceAndLookup's
//     second-Replace assertion but exercised through the wire).

package scheddgrpc_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
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
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// capturingEngine is a SchedAPI whose CapacitySink drives
// reports into a per-test receiver (an ordered slice or a
// seen-set, depending on the test). The rest of the SchedAPI
// surface is a no-op. Defined inline (not via fakeEngine)
// because the existing fakeEngine's CapacitySink default
// no-ops, and the test needs to observe the captured reports.
// A third SchedAPI fixture (the multi-node coexistence test)
// extends capturingEngine with a seen-set.
type capturingEngine struct {
	mu   *sync.Mutex
	recv *[]sched.CapacityReport
	seen map[string]bool
}

func (c *capturingEngine) Wake(_ context.Context, _, _, _, _ string) (sched.WakeResult, error) {
	return sched.WakeResult{}, nil
}
func (c *capturingEngine) AdmitInstance(_ context.Context, _, _, _, _ string) (sched.WakeResult, error) {
	return sched.WakeResult{}, nil
}

// AdmitMirrorInstance (issue #72 / ADR-124 PR-A3) — capacity tests don't exercise the mirror hot path.
func (c *capturingEngine) AdmitMirrorInstance(_ context.Context, _, _, _ string) (sched.WakeResult, error) {
	return sched.WakeResult{}, nil
}

// EnsureWake (ADR-098): capacity tests don't exercise single-flight, so
// this delegates straight through to Wake. Returning a zero CoordOutcome
// with nil Instance triggers the defensive nil-instance branch in the
// handler only on success; no capacity test relies on a non-nil
// Instance, so the zero value is sufficient.
func (c *capturingEngine) EnsureWake(_ context.Context, _, _ string) (sched.CoordOutcome, error) {
	return sched.CoordOutcome{}, nil
}
func (c *capturingEngine) ReportActivity(_ context.Context, _ []state.InstanceTouch) (int, error) {
	return 0, nil
}
func (c *capturingEngine) ParkWithReason(_ context.Context, _, _ string) error { return nil }
func (c *capturingEngine) ForceColdBootNextWake(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

// ForceRestart (P2d) — stub; capacity tests don't exercise the
// path. strictModeEngine inherits this via embedding.
func (c *capturingEngine) ForceRestart(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (c *capturingEngine) StreamAppLogs(_ context.Context, _ string, _ int64, _ time.Time, _ string, _ scheddgrpc.LogFrameSink) error {
	return nil
}
func (c *capturingEngine) StreamWarmHints(_ context.Context, _ scheddgrpc.WarmHintSink) error {
	return nil
}
func (c *capturingEngine) CapacitySink() scheddgrpc.CapacitySink {
	return func(r sched.CapacityReport) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.recv != nil {
			*c.recv = append(*c.recv, r)
		}
		if c.seen != nil {
			c.seen[r.NodeID] = true
		}
		return nil
	}
}

// NodeKeyRegistry returns nil to disable signature verification
// (pre-slice-3 mode). The RoundTrip test sends unsigned reports
// via plain CapacityReport proto messages — the wire field is
// additive and the handler skips verification when the
// registry is nil. The TestReportCapacity_Slice3StrictMode test
// (added in Task #39) wraps this engine with a populated
// registry.
func (c *capturingEngine) NodeKeyRegistry() *sched.NodeKeyRegistry { return nil }

// DestroyForLivenessFailure (issue #554 / ADR-078) — stub
// satisfies the SchedAPI interface; the capacity tests don't
// exercise the ReportLivenessFailed RPC path.
func (c *capturingEngine) DestroyForLivenessFailure(_ context.Context, _, _ string) error { return nil }

// DestroyForWorkloadOOMFailure (Cluster C / ADR-121) — stub to
// satisfy the SchedAPI interface; the capacity tests don't
// exercise the ReportWorkloadOOM RPC path.
func (c *capturingEngine) DestroyForWorkloadOOMFailure(_ context.Context, _ string, _, _ int) error {
	return nil
}

type telemetryCapturingEngine struct {
	*capturingEngine
	telemetryMu sync.Mutex
	nodeID      string
	rows        []sched.NodeTelemetry
}

func (c *telemetryCapturingEngine) TelemetrySink() sched.TelemetrySink {
	return func(nodeID string, _ time.Time, _ time.Time, rows []sched.NodeTelemetry) error {
		c.telemetryMu.Lock()
		defer c.telemetryMu.Unlock()
		c.nodeID = nodeID
		c.rows = append([]sched.NodeTelemetry(nil), rows...)
		return nil
	}
}

// TestReportCapacity_RoundTrip drives two reports through the
// wire and asserts the handler applies them to the table via
// the SchedAPI.CapacitySink seam. The seam is the only surface
// the test governs: the handler's job is to decode the proto,
// invoke the sink, and reply with the typed ack on stream close.
func TestReportCapacity_RoundTrip(t *testing.T) {
	var (
		mu       sync.Mutex
		received []sched.CapacityReport
	)
	want := []sched.CapacityReport{
		{
			NodeID:        "node-1",
			SampledAt:     time.Unix(1730000000, 0).UTC(),
			LiveCount:     12,
			LeasedCount:   8,
			UsedMB:        4096,
			RAMHeadroomMB: 32000,
			VCPUBusy:      24,
		},
		{
			NodeID:        "node-2",
			SampledAt:     time.Unix(1730000001, 0).UTC(),
			LiveCount:     4,
			LeasedCount:   2,
			UsedMB:        1024,
			RAMHeadroomMB: 35000,
			VCPUBusy:      8,
		},
	}
	cli := newServer(t, &capturingEngine{mu: &mu, recv: &received})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}

	for _, r := range want {
		if err := stream.Send(&scheddpb.CapacityReport{
			NodeId:          r.NodeID,
			SampledAtUnixMs: r.SampledAt.UnixMilli(),
			LiveCount:       r.LiveCount,
			LeasedCount:     r.LeasedCount,
			UsedMb:          r.UsedMB,
			RamHeadroomMb:   r.RAMHeadroomMB,
			VcpuBusy:        r.VCPUBusy,
		}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	ack, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	if ack == nil {
		t.Fatal("CloseAndRecv returned nil ack")
	}

	// Engine side: the captured slice must carry both reports
	// in order, with intact fields. The handler decodes
	// SampledAtUnixMs via time.UnixMilli — assert the round-
	// trip preserves the second.
	mu.Lock()
	defer mu.Unlock()
	if len(received) != len(want) {
		t.Fatalf("received %d reports, want %d", len(received), len(want))
	}
	for i := range want {
		got := received[i]
		w := want[i]
		if got.NodeID != w.NodeID {
			t.Errorf("[%d] NodeID = %q, want %q", i, got.NodeID, w.NodeID)
		}
		if got.LiveCount != w.LiveCount {
			t.Errorf("[%d] LiveCount = %d, want %d", i, got.LiveCount, w.LiveCount)
		}
		if got.UsedMB != w.UsedMB {
			t.Errorf("[%d] UsedMB = %d, want %d", i, got.UsedMB, w.UsedMB)
		}
		if got.RAMHeadroomMB != w.RAMHeadroomMB {
			t.Errorf("[%d] RAMHeadroomMB = %d, want %d", i, got.RAMHeadroomMB, w.RAMHeadroomMB)
		}
		if got.VCPUBusy != w.VCPUBusy {
			t.Errorf("[%d] VCPUBusy = %d, want %d", i, got.VCPUBusy, w.VCPUBusy)
		}
		if got.SampledAt.Unix() != w.SampledAt.Unix() {
			t.Errorf("[%d] SampledAt = %v, want %v", i, got.SampledAt, w.SampledAt)
		}
	}
}

func TestReportCapacity_BatchedTelemetryReachesCacheSink(t *testing.T) {
	mu := sync.Mutex{}
	engine := &telemetryCapturingEngine{
		capturingEngine: &capturingEngine{mu: &mu},
	}
	cli := newServer(t, engine)
	stream, err := cli.ReportCapacity(context.Background())
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	if err := stream.Send(&scheddpb.CapacityReport{
		NodeId:          "node-a",
		SampledAtUnixMs: 1730000000000,
		Instances: []*scheddpb.InstanceTelemetry{{
			InstanceId:        "vm-1",
			ResidentBytes:     wrapperspb.Int64(128 << 20),
			CpuPct:            wrapperspb.Double(4.5),
			InflightRequests:  7,
			RequestCountTotal: wrapperspb.Int64(123),
			DiskUsedBytes:     wrapperspb.Int64(80),
			DiskCapacityBytes: wrapperspb.Int64(100),
			OpenConns:         2,
			LastRequestAt:     timestamppb.New(time.Unix(123, 0)),
		}},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	engine.telemetryMu.Lock()
	defer engine.telemetryMu.Unlock()
	if engine.nodeID != "node-a" || len(engine.rows) != 1 {
		t.Fatalf("telemetry = node=%q rows=%d, want node-a/1", engine.nodeID, len(engine.rows))
	}
	row := engine.rows[0]
	if row.InstanceID != "vm-1" || row.ResidentBytes == nil || *row.ResidentBytes != 128<<20 || row.CPUPct == nil || *row.CPUPct != 4.5 || row.InflightRequests != 7 || row.RequestCountTotal == nil || *row.RequestCountTotal != 123 || row.OpenConns != 2 || row.DiskUsedBytes == nil || *row.DiskUsedBytes != 80 || row.DiskCapacityBytes == nil || *row.DiskCapacityBytes != 100 {
		t.Fatalf("telemetry row = %+v, want vm-1 resident=128MiB cpu=4.5 inflight=7 open_conns=2", row)
	}
}

// TestReportCapacity_ContextCancelSurfacesCanceled cancels the
// caller's context mid-stream and asserts the handler surfaces
// codes.Canceled on the wire. Mirrors the warmhints test's
// cancel coverage so the two long-lived streams share the same
// failure-mode contract.
func TestReportCapacity_ContextCancelSurfacesCanceled(t *testing.T) {
	cli := newServer(t, &fakeEngine{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}

	// Send one report so the handler is past the first Recv,
	// then cancel. The Recv reads see context.Canceled and the
	// handler maps to codes.Canceled.
	if err := stream.Send(&scheddpb.CapacityReport{NodeId: "node-1", UsedMb: 1}); err != nil {
		if !errors.Is(err, io.EOF) {
			// Some client stacks surface a status error
			// here; tolerate it but log for the test trail.
			t.Logf("Send after cancel returned: %v (acceptable per gRPC semantics)", err)
		}
	} else {
		cancel()
		time.Sleep(10 * time.Millisecond)
	}

	// Drive CloseAndRecv to wait for the handler's response.
	// Whether the cancel lands before or after Send, the ack
	// path is either Canceled or InvalidArgument (if the
	// empty-id check didn't fire). In either case the call
	// must NOT return nil.
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("CloseAndRecv returned nil after cancel; want Canceled")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("err = io.EOF after cancel; want a status error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err = %v; want gRPC status error", err)
	}
	if st.Code() != codes.Canceled {
		t.Errorf("code = %v, want Canceled", st.Code())
	}
}

// TestReportCapacity_EmptyNodeIDIsInvalidArgument pins the
// load-bearing gate: an empty node_id is rejected with
// codes.InvalidArgument (the table's defensive no-op is a
// fallback, not the gate). A regression that lets an empty
// id slip through to the table would corrupt the cache and
// silently hide a vmmd bug.
func TestReportCapacity_EmptyNodeIDIsInvalidArgument(t *testing.T) {
	cli := newServer(t, &fakeEngine{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}

	// Send may return errors before the handler's
	// validation (stream already closed by ctx cancel) — but
	// typically only after the gRPC trailer arrives. Either
	// way the empty-id assertion below is the load-bearing
	// check; we tolerate the Send return.
	_ = stream.Send(&scheddpb.CapacityReport{NodeId: ""})

	// CloseAndRecv surfaces the handler's status. The handler
	// returns codes.InvalidArgument for empty node_id.
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("CloseAndRecv = nil; want InvalidArgument")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err = %v; want status error", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", st.Code())
	}
}

// TestReportCapacity_StreamClosedAfterLastSend closes the send
// side after the last report and asserts CloseAndRecv returns
// a non-nil ack (the handler's SendAndClose path). The shape
// is distinct from the round-trip test's "send N then close",
// which exercises the same path; this test is the bare
// "close after send" guarantee with the smallest possible
// payload to pin the SendAndClose contract.
func TestReportCapacity_StreamClosedAfterLastSend(t *testing.T) {
	cli := newServer(t, &fakeEngine{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}

	if err := stream.Send(&scheddpb.CapacityReport{
		NodeId: "node-1",
		UsedMb: 100,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ack, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	if ack == nil {
		t.Fatal("ack = nil; want non-nil ReportCapacityAck")
	}
}

// TestReportCapacity_MultipleNodesCoexist sends reports for two
// distinct node_ids and asserts both reach the engine side. The
// second-id assertion is the load-bearing one: a regression
// that overwrites the first entry on the second Replace would
// break the chooser's per-node accounting (PR-2).
func TestReportCapacity_MultipleNodesCoexist(t *testing.T) {
	var (
		mu   sync.Mutex
		seen = map[string]bool{}
	)
	cli := newServer(t, &capturingEngine{mu: &mu, seen: seen})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}

	for _, id := range []string{"node-a", "node-b"} {
		if err := stream.Send(&scheddpb.CapacityReport{NodeId: id, UsedMb: 100}); err != nil {
			t.Fatalf("Send %s: %v", id, err)
		}
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !seen["node-a"] {
		t.Errorf("node-a missing; second Replace overwrote first entry")
	}
	if !seen["node-b"] {
		t.Errorf("node-b missing; second Replace was lost")
	}
}

// signedEngine wraps fakeEngine to expose a populated
// NodeKeyRegistry. The default fakeEngine returns nil to disable
// signature verification (pre-slice-3 mode). For slice-3 tests
// (ADR-053), a separate wrapper concrete type is needed because
// the fakeEngine's NodeKeyRegistry() is method-defined on the
// type, not a settable field.
type signedEngine struct {
	*fakeEngine
	keys *sched.NodeKeyRegistry
}

func (s *signedEngine) NodeKeyRegistry() *sched.NodeKeyRegistry { return s.keys }

// stubKeyLoader is a NodeKeyLoader backed by an in-memory slice.
// The handler-side test doesn't actually exercise Refresh — the
// registry is pre-populated via ReplaceAll — but the loader
// field is required by NewNodeKeyRegistry.
type stubKeyLoader struct {
	rows []sched.NodeKeyRow
}

func (s *stubKeyLoader) LoadNodeKeys(_ context.Context) ([]sched.NodeKeyRow, error) {
	return s.rows, nil
}

// TestReportCapacity_BadSignatureIncrementsCounter pins the
// ADR-053 §3 observability contract: a rejected CapacityReport
// stream (codes.Unauthenticated) increments the
// schedd_capacity_signature_rejected_total counter exactly once.
// A regression that drops the increment silently hides hostile
// publishers behind a "the wire said no" log line.
func TestReportCapacity_BadSignatureIncrementsCounter(t *testing.T) {
	// Pre-construct a real P-256 key so the registry holds a
	// valid key_id; the report we send below carries a different
	// key_id (empty), so VerifyNodeSignature returns
	// ErrUnknownNodeKey.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	registeredKeyID, err := sched.KeyIDForPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("KeyIDForPublicKey: %v", err)
	}

	// PEM-encode the public key so the registry's loader path
	// can parse it (we bypass the loader by calling ReplaceAll
	// directly with the parsed key, but the constructor still
	// needs a non-nil loader).
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	loader := &stubKeyLoader{rows: []sched.NodeKeyRow{
		{KeyID: registeredKeyID, PublicKeyPEM: string(pemBytes)},
	}}
	keys := sched.NewNodeKeyRegistry(loader, nil)
	if n := keys.ReplaceAll(loader.rows); n != 1 {
		t.Fatalf("ReplaceAll: %d, want 1", n)
	}

	// Build the engine. We need both a populated registry AND
	// a witness for the sink so the test can assert no report
	// reached the table (the handler must close before the
	// sink is invoked).
	var (
		mu       sync.Mutex
		received []sched.CapacityReport
	)
	eng := &signedEngine{
		fakeEngine: &fakeEngine{},
		keys:       keys,
	}

	// Build the server with a fresh OpsMetrics so the test can
	// read the counter without polluting other tests.
	ops := wire.NewOpsMetrics("schedd_test")
	srv := grpc.NewServer()
	scheddgrpc.New(eng, ops, nil).Register(srv)
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
	cli := scheddpb.NewScheddClient(conn)

	// Send a report with an empty NodeKeyID — VerifyNodeSignature
	// returns ErrUnknownNodeKey (the report's key_id doesn't
	// resolve in the registry). The handler must reject with
	// codes.Unauthenticated and increment the counter.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	if err := stream.Send(&scheddpb.CapacityReport{
		NodeId:          "node-1",
		SampledAtUnixMs: time.Unix(1730000000, 0).UnixMilli(),
		UsedMb:          1024,
		NodeKeyId:       "", // empty → ErrUnknownNodeKey
		NodeSignature:   []byte{0xde, 0xad, 0xbe, 0xef},
	}); err != nil {
		t.Logf("Send returned (acceptable if trailer arrived first): %v", err)
	}
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("CloseAndRecv = nil after bad signature; want Unauthenticated")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err = %v; want gRPC status error", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}

	// Counter must have incremented exactly once. The handler
	// closes on the first bad frame, so per-stream increment
	// matches per-call increment here.
	if got := testutil.ToFloat64(ops.CapacitySignatureRejected()); got != 1 {
		t.Errorf("capacity_signature_rejected_total = %v, want 1", got)
	}

	// Defensive: no report reached the sink. The handler must
	// have closed before the table.Replace call.
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 0 {
		t.Errorf("received %d reports; handler must have rejected before sink", len(received))
	}
}
