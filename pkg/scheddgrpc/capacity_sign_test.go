// capacity_sign_test.go — ADR-053 slice-3 strict-mode tests for
// the ReportCapacity gRPC handler.
//
// Pins the behavior the schedd-side handler enforces when a
// NodeKeyRegistry is wired (slice-3 mode):
//
//  1. Signed report from a registered key → accepted, lands in
//     the engine via the CapacitySink seam.
//  2. Unsigned report on slice-3 schedd → codes.Unauthenticated,
//     the stream closes (rejection is loud, not silent).
//  3. Tampered payload (NodeID changed after signing) →
//     codes.Unauthenticated; the stream closes.
//  4. Unknown key_id (registry has the row but not for this
//     caller) → codes.Unauthenticated; the stream closes.
//  5. Pre-slice-3 schedd (registry == nil) accepts unsigned
//     reports — the additive wire field is backward-compatible.
//
// The tests use a wrapping engine that exposes a real
// sched.NodeKeyRegistry populated by a stub loader. The
// production loader is Postgres-backed; tests inject a
// fixed-key loader so the suite stays portable.

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
	"sync"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// x509MarshalPKIX marshals an ECDSA public key to PKIX then
// wraps it in a PEM "PUBLIC KEY" block. Mirrors the format
// that parsePublicKeyPEM (pkg/sched/nodekeys.go) accepts.
func x509MarshalPKIX(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// stubLoader populates the NodeKeyRegistry from a fixed
// in-memory map. Real production wiring hits Postgres
// (migrations/00075) and listens to compute_node_changed
// pg_notify; tests inject this stub so the suite is
// table-driven and doesn't require DB.
type stubLoader struct {
	rows []sched.NodeKeyRow
}

func (s *stubLoader) LoadNodeKeys(_ context.Context) ([]sched.NodeKeyRow, error) {
	out := make([]sched.NodeKeyRow, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

// strictModeEngine wraps capturingEngine and exposes a real
// NodeKeyRegistry populated with the test's expected keys.
// Captures both (a) the reports the handler accepts and
// (b) whether the stream rejected (via the registry seeing
// at least one frame).
type strictModeEngine struct {
	*capturingEngine
	registry *sched.NodeKeyRegistry
}

func (s *strictModeEngine) NodeKeyRegistry() *sched.NodeKeyRegistry {
	return s.registry
}

// freshStrictModeRegistry returns a registry pre-loaded with
// one well-known key. Returns the registry + the matching
// (priv, keyID) pair so the test can stamp the report on the
// wire.
func freshStrictModeRegistry(t *testing.T) (*sched.NodeKeyRegistry, *ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	keyID, err := sched.KeyIDForPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("KeyIDForPublicKey: %v", err)
	}
	der, err := x509MarshalPKIX(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	loader := &stubLoader{
		rows: []sched.NodeKeyRow{
			{ComputeNodeID: "node-1", KeyID: keyID, PublicKeyPEM: string(der)},
		},
	}
	reg := sched.NewNodeKeyRegistry(loader, nil)
	if n, err := reg.Refresh(context.Background()); err != nil || n != 1 {
		t.Fatalf("registry init: n=%d err=%v", n, err)
	}
	return reg, priv, keyID
}

// signCapacityReport is the dual of sched.SignNodeReport that
// builds a *scheddpb.CapacityReport with the proto's
// node_signature + node_key_id fields stamped. Lives here
// (not pkg/sched) so the test reflects exactly what the vmmd
// publisher would emit on the wire.
func signCapacityReport(t *testing.T, priv *ecdsa.PrivateKey, keyID, nodeID string, sampledAt time.Time, usedMB int32) *scheddpb.CapacityReport {
	t.Helper()
	r := sched.CapacityReport{
		NodeID:        nodeID,
		SampledAt:     sampledAt,
		LiveCount:     1,
		UsedMB:        usedMB,
		RAMHeadroomMB: 32000,
		VCPUBusy:      2,
	}
	sig, err := sched.SignNodeReport(priv, r)
	if err != nil {
		t.Fatalf("SignNodeReport: %v", err)
	}
	return &scheddpb.CapacityReport{
		NodeId:          nodeID,
		SampledAtUnixMs: sampledAt.UnixMilli(),
		LiveCount:       r.LiveCount,
		UsedMb:          r.UsedMB,
		RamHeadroomMb:   r.RAMHeadroomMB,
		VcpuBusy:        r.VCPUBusy,
		NodeSignature:   sig,
		NodeKeyId:       keyID,
	}
}

// TestReportCapacity_Slice3_SignedReportAccepted pins the
// happy path: a report stamped with a valid ECDSA signature
// from a registered key is accepted by the slice-3 handler
// and lands in the engine via the CapacitySink seam.
func TestReportCapacity_Slice3_SignedReportAccepted(t *testing.T) {
	reg, priv, keyID := freshStrictModeRegistry(t)
	var (
		mu       sync.Mutex
		received []sched.CapacityReport
	)
	cli := newServer(t, &strictModeEngine{
		capturingEngine: &capturingEngine{mu: &mu, recv: &received},
		registry:        reg,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	report := signCapacityReport(t, priv, keyID, "node-1", time.UnixMilli(1730000000000), 4096)
	if err := stream.Send(report); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("received %d reports, want 1", len(received))
	}
	if received[0].NodeID != "node-1" {
		t.Errorf("NodeID = %q, want node-1", received[0].NodeID)
	}
	if received[0].UsedMB != 4096 {
		t.Errorf("UsedMB = %d, want 4096", received[0].UsedMB)
	}
}

// TestReportCapacity_Slice3_UnsignedReportRejected pins the
// rejection path: a slice-3 schedd rejects an unsigned report
// with codes.Unauthenticated and closes the stream. The
// rejection is whole-stream (not per-frame) so an attacker
// can't DoS by injecting one valid frame + 1000 garbage ones.
func TestReportCapacity_Slice3_UnsignedReportRejected(t *testing.T) {
	reg, _, _ := freshStrictModeRegistry(t)
	cli := newServer(t, &strictModeEngine{
		capturingEngine: &capturingEngine{},
		registry:        reg,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	// Empty signature → ErrEmptySignature on the slice-3 handler.
	if err := stream.Send(&scheddpb.CapacityReport{
		NodeId:          "node-1",
		SampledAtUnixMs: time.Now().UnixMilli(),
		UsedMb:          4096,
	}); err != nil && !errors.Is(err, io.EOF) {
		t.Logf("Send (acceptable to fail mid-rejection): %v", err)
	}
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("CloseAndRecv = nil; want Unauthenticated")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err = %v; want status error", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

// TestReportCapacity_Slice3_TamperedPayloadRejected: a report
// stamped with a valid signature but a different NodeID after
// signing breaks the signature. The slice-3 handler maps this
// to codes.Unauthenticated and rejects the stream.
//
// We sign the report with node-1, then send the SAME signature
// bytes under node-2 (the canonical payload includes node_id,
// so the digest changes).
func TestReportCapacity_Slice3_TamperedPayloadRejected(t *testing.T) {
	reg, priv, keyID := freshStrictModeRegistry(t)
	cli := newServer(t, &strictModeEngine{
		capturingEngine: &capturingEngine{},
		registry:        reg,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	// Sign for node-1, send the signature on a node-2 frame.
	signed := signCapacityReport(t, priv, keyID, "node-1", time.UnixMilli(1730000000000), 4096)
	tampered := &scheddpb.CapacityReport{
		NodeId:          "node-2", // different from the signed report
		SampledAtUnixMs: signed.SampledAtUnixMs,
		LiveCount:       signed.LiveCount,
		UsedMb:          signed.UsedMb,
		RamHeadroomMb:   signed.RamHeadroomMb,
		VcpuBusy:        signed.VcpuBusy,
		NodeSignature:   signed.NodeSignature,
		NodeKeyId:       signed.NodeKeyId,
	}
	if err := stream.Send(tampered); err != nil && !errors.Is(err, io.EOF) {
		t.Logf("Send (acceptable to fail mid-rejection): %v", err)
	}
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("CloseAndRecv = nil; want Unauthenticated")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err = %v; want status error", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

// TestReportCapacity_Slice3_UnknownKeyIDRejected: a valid
// signature from a key that the registry doesn't carry for
// this node_id is rejected. Distinct from "wrong node" — the
// signature is correct, but the registry has no row.
func TestReportCapacity_Slice3_UnknownKeyIDRejected(t *testing.T) {
	reg, priv, keyID := freshStrictModeRegistry(t)
	// Populate the registry with a DIFFERENT key.
	_ = priv
	_ = keyID
	otherPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	otherKeyID, err := sched.KeyIDForPublicKey(&otherPriv.PublicKey)
	if err != nil {
		t.Fatalf("KeyIDForPublicKey: %v", err)
	}
	der, err := x509MarshalPKIX(&otherPriv.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	reg.ReplaceAll([]sched.NodeKeyRow{
		{ComputeNodeID: "node-1", KeyID: otherKeyID, PublicKeyPEM: string(der)},
	})
	cli := newServer(t, &strictModeEngine{
		capturingEngine: &capturingEngine{},
		registry:        reg,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	// Sign with the original key (not in the registry any
	// more after ReplaceAll). The handler must reject.
	report := signCapacityReport(t, priv, keyID, "node-1", time.UnixMilli(1730000000000), 4096)
	if err := stream.Send(report); err != nil && !errors.Is(err, io.EOF) {
		t.Logf("Send (acceptable to fail mid-rejection): %v", err)
	}
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("CloseAndRecv = nil; want Unauthenticated")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err = %v; want status error", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

// TestReportCapacity_PreSlice3_AcceptsUnsigned pins the
// backward-compatibility property: a pre-slice-3 schedd
// (registry == nil) accepts unsigned reports without error.
// The wire field is additive, so a legacy vmmd talking to a
// slice-3 schedd (whose registry is nil) keeps working.
func TestReportCapacity_PreSlice3_AcceptsUnsigned(t *testing.T) {
	var (
		mu       sync.Mutex
		received []sched.CapacityReport
	)
	cli := newServer(t, &capturingEngine{mu: &mu, recv: &received})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	if err := stream.Send(&scheddpb.CapacityReport{
		NodeId:          "node-1",
		SampledAtUnixMs: time.UnixMilli(1730000000000).UnixMilli(),
		UsedMb:          4096,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Errorf("received %d reports, want 1 (unsigned accepted on pre-slice-3)", len(received))
	}
}

// unused import guard for state package (the capturingEngine
// embeds it transitively via interface compliance).
var _ = state.InstanceTouch{}
