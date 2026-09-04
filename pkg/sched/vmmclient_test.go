// vmmclient_test.go — exercises the typed vmmd wrapper end-to-end against the
// real pkg/vmmdgrpc.Server over bufconn, mirroring pkg/vmmdgrpc/bufconn_test.go.
// A fake VmmdAPI stands in for firecracker so the wire path (proto round-trip +
// error re-lifting) is fully covered without KVM.

package sched_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeVMM is the server-side VmmdAPI (pkg/vmmdgrpc.VmmdAPI). It mirrors the
// resource shape of pkg/fcvm.Manager so the handlers take no test-only branch.
type fakeVMM struct {
	wakeFn func(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error)
	parkFn func(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
	destFn func(ctx context.Context, instance string) error
}

func (f *fakeVMM) Wake(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
	if f.wakeFn != nil {
		return f.wakeFn(ctx, req)
	}
	return &fcvm.Instance{
		Lease: fcvm.Lease{
			Instance: req.Instance,
			UID:      20001,
			GID:      20001,
			HostIP:   netip.MustParseAddr("10.100.0.2"),
			Netns:    "fc-" + req.Instance,
			VethHost: "vh1",
			VethPeer: "vp1",
		},
		Net: netns.Config{
			Netns:    "fc-" + req.Instance,
			VethHost: "vh1",
			VethPeer: "vp1",
		},
		Method: fcvm.WakeColdBoot,
	}, nil
}

func (f *fakeVMM) Park(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	if f.parkFn != nil {
		return f.parkFn(ctx, instance, spec)
	}
	return fcvm.SnapshotInfo{MemBytes: 130 * 1024 * 1024, VMStateBytes: 4096}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the vmmclient
// test's no-op seam — the vmmclient_test fake implements
// vmmdgrpc.VmmdAPI for the bufconn round-trip wire test, not
// the engine's captureWarmSnapshotLocked path (which lives in
// engine_test). The fake's Park handles the legacy PauseAndSnapshot
// RPC; WarmSnapshot does the same for the new RPC.
func (f *fakeVMM) WarmSnapshot(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	return fcvm.SnapshotInfo{MemBytes: 130 * 1024 * 1024, VMStateBytes: 4096}, nil
}

func (f *fakeVMM) Destroy(ctx context.Context, instance string) error {
	if f.destFn != nil {
		return f.destFn(ctx, instance)
	}
	return nil
}

// StopInstance (M-2 / ADR-138 §Decision 1) is the graceful
// signal-then-grace-then-SIGKILL stop sequence. Test fakes
// default to no-op + nil — the engine's per-mode dispatch lives
// in pkg/sched/engine_stop_pgtest_test.go (commit 6).
func (f *fakeVMM) StopInstance(_ context.Context, _ string, _, _ int32) (*sched.StopInstanceOutcome, error) {
	return nil, nil
}

func (f *fakeVMM) DestroyWithExport(ctx context.Context, instance, exportDir string) (int, error) {
	// Schedd doesn't use the export path; treat as Destroy-equivalent.
	if f.destFn != nil {
		if err := f.destFn(ctx, instance); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

// SignalAndKill (M-2 / ADR-138 §Decision 1) is the graceful
// stop sequence. Test fakes default to no-op + (false, 0, nil)
// — the bufconn wire round-trip test asserts the gRPC envelope
// shape and lift behaviour, not the inner teardown. Behavioural
// coverage lives in pkg/fcvm/vmm_signal_kill_test.go (portable).
func (f *fakeVMM) SignalAndKill(_ context.Context, _ string, _ int32, _ int32) (bool, int32, error) {
	return false, 0, nil
}

func (f *fakeVMM) ExportDirFor(instance string) string { return "" }

func (f *fakeVMM) LiveCount() int   { return 0 }
func (f *fakeVMM) LeasedCount() int { return 0 }

// NetnsFor is the issue #98 / ADR-028 bridge to vmmd's
// ForwardHTTP handler. The sched test rig never invokes
// ForwardHTTP, so we return the not-live (false) default — any
// test that needs the bridge will wire netnsFn itself.
func (f *fakeVMM) NetnsFor(instance string) (string, bool) { return "", false }

// UpdateEgressAllowlist (tier-2 PR-B) — the sched test rig
// doesn't drive the in-place patch path; the egress_drift
// subscriber tests in pkg/sched/egress_drift_test.go use a
// separate seam (RoutedVMM.UpdateEgressAllowlist). Returns nil
// here so the vmmdgrpc.VmmdAPI contract is satisfied.
func (f *fakeVMM) UpdateEgressAllowlist(ctx context.Context, appID string, allowlist []netip.Prefix) error {
	return nil
}

// UpdateStaticEgressIP (ADR-119) is the no-op test fake.
// The drift subscriber tests in pkg/sched/egress_drift_test.go
// use the RoutedVMM seam, not the gRPC client seam.
func (f *fakeVMM) UpdateStaticEgressIP(ctx context.Context, accountID, appID string, ip string) error {
	return nil
}

// Logs (issue #254 / Move 4) — the sched test rig doesn't drive
// the per-instance log stream; the Move 4 handler tests live in
// pkg/scheddgrpc and inject a real pkg/vmmdgrpc Server over
// bufconn. Returns a closed sched.LogStream so the caller's Recv
// sees EOF immediately (the right no-op for tests that don't care
// about log content). PR-B adds the sinceWrittenAt time lower-bound;
// the fake ignores it.
func (f *fakeVMM) Logs(_ context.Context, _ string, _ int64, _ time.Time) (sched.LogStream, error) {
	return nil, io.EOF
}

// InstancePID (M8 §11) — the sched test rig never drives the
// SeccompStatus path; cmd/e2e/sec11_seccomp_e2e_test.go dials
// vmmd directly. Returns (0, false) so any test that accidentally
// hits the codepath fails fast with NotFound instead of getting
// a phantom PID.
func (f *fakeVMM) InstancePID(instance string) (int, bool) { return 0, false }

// LogRing (issue #254 / Move 4) — the sched test rig never drives
// the per-instance log stream directly; the vmmdgrpc logs_test.go
// covers the handler. Returns nil so the VmmdAPI contract is
// satisfied; any call from the sched-side path that hits this
// will surface as a NotFound from vmmd's handler.
func (f *fakeVMM) LogRing(_ string) *logbuf.Ring { return nil }

// MountParentExt4 (ADR-053) — the sched test rig never drives
// the parent-mount staging path; imaged owns those gRPC calls.
// Returns "" + nil so the VmmdAPI contract is satisfied and any
// accidental invocation would surface as imaged's
// "empty mountpoint" check rather than a NotFound from vmmd.
func (f *fakeVMM) MountParentExt4(_ context.Context, _ string) (string, error) {
	return "", nil
}

// MaterializeParentExt4 is owned by imaged's staging path; sched never calls
// it, but the server-side API must remain complete for the bufconn harness.
func (f *fakeVMM) MaterializeParentExt4(_ context.Context, _, _ string) error {
	return nil
}

// UmountParentExt4 (ADR-053) — sched doesn't drive the parent
// umount path. Returns nil; imaged's defer-on-error pattern is
// idempotent on this no-op success.
func (f *fakeVMM) UmountParentExt4(_ context.Context, _ string) error {
	return nil
}

// MountOverlayParent (ADR-075 / DEPLOY-1) — sched never issues
// the overlay mount; imaged is the only caller of the gRPC.
// Returns nil so the vmmdgrpc.VmmdAPI contract is satisfied;
// any accidental sched-side call surfaces as a no-op success
// in the bufconn test rig and an empty-mountpoint error on the
// box.
func (f *fakeVMM) MountOverlayParent(_ context.Context, _, _, _, _ string) error {
	return nil
}

// UmountOverlayParent (ADR-075 / DEPLOY-1) — sched never issues
// the overlay umount; imaged is the only caller. Returns nil;
// the vmmd-side handler is idempotent on unknown mountpoints.
func (f *fakeVMM) UmountOverlayParent(_ context.Context, _ string) error {
	return nil
}

// MarkInstanceFrameworkReady (issue #470 / PR #470-FU-B) — the
// sched-side fakeVMM doesn't drive the warm-capture path; the
// framework-ready receipt is owned by cmd/vmmd's DGRAM host recv
// loop. Returns (true, "", "", nil) so any test that looks up a
// non-existent instance still sees the receipt-accepted shape.
func (f *fakeVMM) MarkInstanceFrameworkReady(_ context.Context, _ string, _ int64) (bool, string, string, error) {
	return true, "", "", nil
}

// newClient stands up a vmmdgrpc.Server on bufconn and returns a sched.VMMClient
// dialed to it.
func newClient(t *testing.T, fake *fakeVMM) *sched.VMMClient {
	t.Helper()
	srv := grpc.NewServer()
	vmmdgrpc.New(fake, wire.NewOpsMetrics("sched_test"), "1.10.0", nil).Register(srv)

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
	c := sched.NewVMMClient(conn)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestVMMClient_CreateColdBoot(t *testing.T) {
	c := newClient(t, &fakeVMM{})
	out, err := c.CreateColdBoot(context.Background(), "i-1", sched.AppSpec{
		BaseKey: "/srv/fc/base", LayerKey: "/srv/fc/layer", VCPUCount: 2, MemSizeMiB: 256,
	})
	if err != nil {
		t.Fatalf("CreateColdBoot: %v", err)
	}
	if out.HostIP != "10.100.0.2" {
		t.Errorf("host_ip = %q, want 10.100.0.2", out.HostIP)
	}
	if out.Netns != "fc-i-1" {
		t.Errorf("netns = %q, want fc-i-1", out.Netns)
	}
	if out.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("method = %v, want WAKE_COLD_BOOT", out.Method)
	}
}

func TestVMMClient_CreateFromSnapshot_FallbackReported(t *testing.T) {
	// Fake always cold-boots; a restore request must report Method=COLD_BOOT
	// but RequestedMethod=RESTORE (ADR-005 fallback surfaced to schedd).
	c := newClient(t, &fakeVMM{})
	out, err := c.CreateFromSnapshot(context.Background(), "i-2",
		sched.AppSpec{BaseKey: "/b", LayerKey: "/l", VCPUCount: 2, MemSizeMiB: 256},
		sched.SnapshotRef{DeploymentID: "d-1", VMStatePath: "/v", FCVersion: "1.10.0", StorageKey: "snap/d-1/mem"},
	)
	if err != nil {
		t.Fatalf("CreateFromSnapshot: %v", err)
	}
	if out.RequestedMethod != vmmdpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("requested = %v, want WAKE_RESTORE", out.RequestedMethod)
	}
	if out.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("method = %v, want WAKE_COLD_BOOT", out.Method)
	}
}

func TestVMMClient_PauseAndSnapshot(t *testing.T) {
	c := newClient(t, &fakeVMM{})
	b, err := c.PauseAndSnapshot(context.Background(), "i-1", "/snap/vmstate", "snap/i-1/mem", "")
	if err != nil {
		t.Fatalf("PauseAndSnapshot: %v", err)
	}
	if b.MemBytes != 130*1024*1024 {
		t.Errorf("mem_bytes = %d", b.MemBytes)
	}
}

func TestVMMClient_PauseAndSnapshot_MissingStorageKey(t *testing.T) {
	c := newClient(t, &fakeVMM{})
	// #121: server validation now requires storage_key always; vmstate
	// is acceptable via EITHER vmstate_path or vmstate_storage_key
	// (issue #121 / ADR-025 axis 2 slice 4). Empty storage_key is
	// still an error so mem F-1 holds; an empty storage_key combined
	// with a populated vmstate_path keeps the legacy single-box path
	// working out of the box (default-local).
	_, err := c.PauseAndSnapshot(context.Background(), "i-1", "/snap/vmstate", "", "snap/d-1/vmstate")
	if err == nil {
		t.Fatal("expected error for empty storage_key")
	}
	// The server rejects with a *api.Problem (CodeValidation); liftErr must
	// re-hydrate it so schedd sees a Problem, not an opaque status.
	var p *api.Problem
	if !errors.As(err, &p) {
		t.Fatalf("error = %T, want *api.Problem", err)
	}
	if p.Code != api.CodeValidation {
		t.Errorf("code = %q, want %q", p.Code, api.CodeValidation)
	}
}

func TestVMMClient_PauseAndSnapshot_AcceptsEitherVmstateLocator(t *testing.T) {
	// #121 / ADR-025 axis 2 slice 4: PauseAndSnapshot accepts a
	// populated vmstate_storage_key alongside an empty vmstate_path
	// (the canonical multi-node shape) and still publishes. The
	// legacy shape (empty vmstate_storage_key, populated
	// vmstate_path) also still validates; this test exercises the
	// new shape so a future regression that re-requires the legacy
	// field trips here.
	c := newClient(t, &fakeVMM{})
	_, err := c.PauseAndSnapshot(context.Background(), "i-1", "", "snap/d-1/mem", "snap/d-1/vmstate")
	if err != nil {
		t.Fatalf("PauseAndSnapshot with empty vmstate_path: %v", err)
	}
}

func TestVMMClient_Destroy_Idempotent(t *testing.T) {
	c := newClient(t, &fakeVMM{})
	if err := c.Destroy(context.Background(), "ghost"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestVMMClient_Wake_ErrorLiftsToProblem(t *testing.T) {
	// A vmmd error carrying an *api.Problem must arrive at schedd as a Problem
	// with its stable code intact (capacity denial → 503 at the gateway).
	c := newClient(t, &fakeVMM{
		wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
			return nil, api.ErrCapacity("no RAM headroom")
		},
	})
	_, err := c.CreateColdBoot(context.Background(), "i-x",
		sched.AppSpec{BaseKey: "/b", LayerKey: "/l", VCPUCount: 2, MemSizeMiB: 256})
	if err == nil {
		t.Fatal("expected capacity error")
	}
	var p *api.Problem
	if !errors.As(err, &p) {
		t.Fatalf("error = %T, want *api.Problem", err)
	}
	if p.Code != api.CodeCapacity {
		t.Errorf("code = %q, want %q", p.Code, api.CodeCapacity)
	}
}

func TestDialVMM_EmptyPath(t *testing.T) {
	if _, err := sched.DialVMM(""); err == nil {
		t.Fatal("expected error for empty socket path")
	}
}
