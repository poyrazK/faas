// server_static_egress_ip_test.go — ADR-119 / ADR-119 v2 unit
// tests for the UpdateStaticEgressIP gRPC handler.
//
// Pins:
//   - empty app_id returns InvalidArgument (CodeValidation).
//   - node_id mismatch returns FailedPrecondition (the
//     defence-in-depth gate; vmmd refuses to apply the pin
//     to the wrong node's live netns).
//   - matching node_id passes through to Manager.
//   - empty node_id on the wire + empty s.nodeID skips the
//     validation (legacy single-box install path).
//   - Manager error surfaces as a gRPC Internal.

package vmmdgrpc

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/wire"
)

// recordingStaticIPVMM is a tiny VmmdAPI that records every
// UpdateStaticEgressIP call. The UpdateStaticEgressIP handler
// tests live in this file because bufconn_test.go's fakeVMM
// is in `package vmmdgrpc_test` and can't be reached from
// here; the only handler we exercise is UpdateStaticEgressIP,
// so a no-op stub of the rest of the VmmdAPI is sufficient.
type recordingStaticIPVMM struct {
	updateStaticIPFn func(ctx context.Context, appID, ip string) error
	calls            []staticIPCall
}

type staticIPCall struct {
	AppID string
	IP    string
}

func (r *recordingStaticIPVMM) UpdateStaticEgressIP(ctx context.Context, appID, ip string) error {
	r.calls = append(r.calls, staticIPCall{AppID: appID, IP: ip})
	if r.updateStaticIPFn != nil {
		return r.updateStaticIPFn(ctx, appID, ip)
	}
	return nil
}

// All other VmmdAPI methods panic — this fake exists solely to
// exercise the UpdateStaticEgressIP handler. A test that drives
// any other surface would fail loudly here, which is the
// intended behaviour.
func (r *recordingStaticIPVMM) Wake(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
	panic("recordingStaticIPVMM.Wake: not used in UpdateStaticEgressIP tests")
}
func (r *recordingStaticIPVMM) Park(context.Context, string, fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	panic("recordingStaticIPVMM.Park: not used in UpdateStaticEgressIP tests")
}
func (r *recordingStaticIPVMM) Destroy(context.Context, string) error {
	panic("recordingStaticIPVMM.Destroy: not used in UpdateStaticEgressIP tests")
}
func (r *recordingStaticIPVMM) DestroyWithExport(context.Context, string, string) (int, error) {
	panic("recordingStaticIPVMM.DestroyWithExport: not used in UpdateStaticEgressIP tests")
}
func (r *recordingStaticIPVMM) LiveCount() int     { return 0 }
func (r *recordingStaticIPVMM) LeasedCount() int   { return 0 }
func (r *recordingStaticIPVMM) NetnsFor(string) (string, bool) {
	return "", false
}
func (r *recordingStaticIPVMM) UpdateEgressAllowlist(context.Context, string, []netip.Prefix) error {
	panic("recordingStaticIPVMM.UpdateEgressAllowlist: not used in UpdateStaticEgressIP tests")
}
func (r *recordingStaticIPVMM) InstancePID(string) (int, bool) { return 0, false }
func (r *recordingStaticIPVMM) LogRing(string) *logbuf.Ring        { return nil }
func (r *recordingStaticIPVMM) MountParentExt4(context.Context, string) (string, error) {
	return "", nil
}
func (r *recordingStaticIPVMM) MaterializeParentExt4(context.Context, string, string) error {
	return nil
}
func (r *recordingStaticIPVMM) UmountParentExt4(context.Context, string) error {
	return nil
}
func (r *recordingStaticIPVMM) MountOverlayParent(context.Context, string, string, string, string) error {
	return nil
}
func (r *recordingStaticIPVMM) UmountOverlayParent(context.Context, string) error {
	return nil
}
func (r *recordingStaticIPVMM) MarkInstanceFrameworkReady(context.Context, string, int64) (bool, string, string, error) {
	return false, "", "", nil
}
func (r *recordingStaticIPVMM) WarmSnapshot(context.Context, string, fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	return fcvm.SnapshotInfo{}, nil
}

// newServerWithNodeID stands up an in-process gRPC server with
// the test-pinned nodeID, dialed over bufconn.
func newServerWithNodeID(t *testing.T, fake *recordingStaticIPVMM, nodeID string) (vmmdpb.VmmdClient, func()) {
	t.Helper()
	ops := wire.NewOpsMetrics("vmmd_test")
	srv := grpc.NewServer()
	impl := NewWithCPUAndNetAndActivityAndNodeID(fake, ops, "1.0.0", nil, nil, nil, nil, nodeID)
	impl.Register(srv)

	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	dialer := grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	})
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		dialer,
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn), srv.Stop
}

// errFakeUpdateStaticEgressIP is the sentinel Manager error
// the fake returns to drive the manager-error branch.
var errFakeUpdateStaticEgressIP = errors.New("fake UpdateStaticEgressIP error")

// TestServer_UpdateStaticEgressIP_Success — happy path:
// node_id matches s.nodeID, Manager is called with the
// expected (appID, ip) pair.
func TestServer_UpdateStaticEgressIP_Success(t *testing.T) {
	t.Parallel()
	rec := &recordingStaticIPVMM{}
	cli, _ := newServerWithNodeID(t, rec, "node-A")
	_, err := cli.UpdateStaticEgressIP(context.Background(), &vmmdpb.UpdateStaticEgressIPRequest{
		AppId:          "app-1",
		StaticEgressIp: "203.0.113.42",
		NodeId:         "node-A",
	})
	if err != nil {
		t.Fatalf("UpdateStaticEgressIP: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(rec.calls))
	}
	if rec.calls[0].AppID != "app-1" {
		t.Errorf("calls[0].AppID = %q, want app-1", rec.calls[0].AppID)
	}
	if rec.calls[0].IP != "203.0.113.42" {
		t.Errorf("calls[0].IP = %q, want 203.0.113.42", rec.calls[0].IP)
	}
}

// TestServer_UpdateStaticEgressIP_EmptyAppID — empty app_id
// returns InvalidArgument (the vmmdpb validation gate).
func TestServer_UpdateStaticEgressIP_EmptyAppID(t *testing.T) {
	t.Parallel()
	rec := &recordingStaticIPVMM{}
	cli, _ := newServerWithNodeID(t, rec, "node-A")
	_, err := cli.UpdateStaticEgressIP(context.Background(), &vmmdpb.UpdateStaticEgressIPRequest{
		AppId:          "",
		StaticEgressIp: "203.0.113.42",
		NodeId:         "node-A",
	})
	if err == nil {
		t.Fatal("want err for empty app_id")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err is not a grpc status: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", st.Code())
	}
	if len(rec.calls) != 0 {
		t.Errorf("Manager called %d times; want 0 (validation fires before Manager)", len(rec.calls))
	}
}

// TestServer_UpdateStaticEgressIP_NodeIDMismatch — defence in
// depth: a wire request whose node_id does NOT match this
// vmmd's node_id is rejected (the wire is well-formed but
// addresses the wrong node — touching the live netns would
// source-spoof egress at the switch). The platform's
// CodeValidation → codes.InvalidArgument mapping (see
// pkg/grpcerr/grpcerr.go::codeToGRPC) keeps the gRPC code
// uniform with the rest of vmmdgrpc's input-rejection
// surface; the Problem's Detail text carries the "node %q
// received UpdateStaticEgressIP for node %q" message that
// distinguishes the wrong-node case from a generic
// validation failure.
//
// Manager must NOT be called.
func TestServer_UpdateStaticEgressIP_NodeIDMismatch(t *testing.T) {
	t.Parallel()
	rec := &recordingStaticIPVMM{}
	cli, _ := newServerWithNodeID(t, rec, "node-A")
	_, err := cli.UpdateStaticEgressIP(context.Background(), &vmmdpb.UpdateStaticEgressIPRequest{
		AppId:          "app-1",
		StaticEgressIp: "203.0.113.42",
		NodeId:         "node-B",
	})
	if err == nil {
		t.Fatal("want err for node_id mismatch")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err is not a grpc status: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument (CodeValidation)", st.Code())
	}
	if !strings.Contains(st.Message(), "node-B") {
		t.Errorf("message should mention the offending node_id (node-B): %q", st.Message())
	}
	if len(rec.calls) != 0 {
		t.Errorf("Manager called %d times; want 0 (defence-in-depth refuses before Manager)", len(rec.calls))
	}
}

// TestServer_UpdateStaticEgressIP_EmptyNodeIDSkiptsValidation —
// the legacy single-box install path: s.nodeID="" AND
// req.NodeId="" both empty skips the validation (no
// defence-in-depth failure on the rollout window where vmmd
// peers are still on the v1 build with no compute_nodes row).
func TestServer_UpdateStaticEgressIP_EmptyNodeIDSkiptsValidation(t *testing.T) {
	t.Parallel()
	rec := &recordingStaticIPVMM{}
	// s.nodeID="" via the legacy constructor (New).
	ops := wire.NewOpsMetrics("vmmd_test")
	srv := grpc.NewServer()
	New(rec, ops, "1.0.0", nil).Register(srv)
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	dialer := grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) { return lis.Dial() })
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		dialer,
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cli := vmmdpb.NewVmmdClient(conn)
	if _, err := cli.UpdateStaticEgressIP(context.Background(), &vmmdpb.UpdateStaticEgressIPRequest{
		AppId:          "app-1",
		StaticEgressIp: "203.0.113.42",
		NodeId:         "",
	}); err != nil {
		t.Fatalf("empty node_id on both sides: err = %v, want nil (legacy single-box path)", err)
	}
	if len(rec.calls) != 1 {
		t.Errorf("calls = %d, want 1", len(rec.calls))
	}
}

// TestServer_UpdateStaticEgressIP_ManagerError — Manager error
// surfaces as a non-OK status. The exact code is determined
// by the grpcerr.Translate path.
func TestServer_UpdateStaticEgressIP_ManagerError(t *testing.T) {
	t.Parallel()
	rec := &recordingStaticIPVMM{}
	rec.updateStaticIPFn = func(_ context.Context, _, _ string) error {
		return errFakeUpdateStaticEgressIP
	}
	cli, _ := newServerWithNodeID(t, rec, "node-A")
	_, err := cli.UpdateStaticEgressIP(context.Background(), &vmmdpb.UpdateStaticEgressIPRequest{
		AppId:          "app-1",
		StaticEgressIp: "203.0.113.42",
		NodeId:         "node-A",
	})
	if err == nil {
		t.Fatal("want err from Manager error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("err is not a grpc status: %v", err)
	}
	if st.Code() == codes.OK {
		t.Errorf("code = OK, want non-OK")
	}
}

// keep imports alive across test rewrites.
var _ = grpc.NewServer
