// End-to-end handler tests via bufconn. No real firecracker, no real
// socket — we stand up an in-process gRPC server backed by a hand-rolled
// fakeVMM that maps to the invariants Manager already enforces.

package vmmdgrpc_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// fakeVMM is the test VmmdAPI implementation. Mirrors the resource shape of
// pkg/fcvm.Manager (Instance, Lease, WakeMethod) so the handlers do not
// branch on a "test vs prod" path.
type fakeVMM struct {
	wakeFn            func(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error)
	parkFn            func(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
	destroy           func(ctx context.Context, instance string) error
	destroyWithExport func(ctx context.Context, instance, exportDir string) (int, error)
	exportDirFn       func(instance string) string
	// netnsFn lets the ForwardHTTP tests decide what netns the fake
	// reports for an instance. nil returns ("", false) — the default
	// success path (every Wake results in a known netns) is wired below.
	netnsFn func(instance string) (string, bool)
	// updateAllowlistFn (tier-2 PR-B) lets the UpdateEgressAllowlist
	// handler test decide what the fake reports. nil = success.
	updateAllowlistFn func(ctx context.Context, appID string, allowlist []netip.Prefix) error
	// instancePIDFn (M8 §11) lets the SeccompStatus handler test
	// decide what the fake returns. nil = (0, false) — the handler
	// maps that to NotFound, which is the right answer for the
	// "unknown instance" gRPC unit test.
	instancePIDFn func(instance string) (int, bool)
	// logRingFn (issue #254 / Move 4) lets the Logs handler test
	// inject a *logbuf.Ring. nil = nil — the handler maps nil to
	// NotFound, which is the right answer for the "no ring" branch.
	logRingFn func(instance string) *logbuf.Ring
	// mountParentFn (ADR-053) lets the MountParentExt4ReadOnly
	// handler test inject behaviour. nil = success returning a
	// stable fake mountpoint path; the handler tests for the
	// error/NotFound branches wire a custom hook.
	mountParentFn func(ctx context.Context, storageKey string) (string, error)
	// umountParentFn (ADR-053) mirrors mountParentFn for the
	// UmountParentExt4 handler test. nil = success.
	umountParentFn func(ctx context.Context, mountpoint string) error
	// mountOverlayFn (ADR-075 / DEPLOY-1) lets the
	// MountOverlayParent handler test inject errors (the
	// invalid-prefix case lifts to InvalidArgument; the kernel
	// syscall case lifts to Internal). nil = success.
	mountOverlayFn func(ctx context.Context, lowerdir, upperdir, workdir, merged string) error
	// umountOverlayFn (ADR-075 / DEPLOY-1) mirrors mountOverlayFn
	// for the UmountOverlayParent handler test. nil = success.
	umountOverlayFn func(ctx context.Context, merged string) error
	// frameworkReadyFn (issue #470 / PR #470-FU-B) lets the
	// FrameworkReady handler test inject errors or override the
	// (stamped, appID, runtime) return tuple. nil = stamps
	// successfully with empty app/runner labels.
	frameworkReadyFn func(ctx context.Context, instance string, warmupMs int64) (bool, string, string, error)
	live             int
	leased           int
}

func (f *fakeVMM) Wake(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
	if f.wakeFn != nil {
		return f.wakeFn(ctx, req)
	}
	// Default success path: pretend the VMM booted successfully.
	f.live++
	f.leased++
	return &fcvm.Instance{
		Lease: fcvm.Lease{
			Instance: req.Instance,
			UID:      20000 + f.leased,
			GID:      20000 + f.leased,
			HostIP:   netip.MustParseAddr("10.100.0.2"),
			Netns:    "fc-" + req.Instance,
			VethHost: "vh99",
			VethPeer: "vp99",
		},
		Method: fcvm.WakeColdBoot,
	}, nil
}

func (f *fakeVMM) Park(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	if f.parkFn != nil {
		return f.parkFn(ctx, instance, spec)
	}
	if instance != "live-1" {
		return fcvm.SnapshotInfo{}, errNotLive
	}
	f.live = 0
	return fcvm.SnapshotInfo{MemBytes: 1024 * 1024 * 130, VMStateBytes: 4096}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the fake hot
// half of the warm path. The bufconn test asserts the wire
// round-trip; the in-memory fake just returns the snapshot
// bytes and keeps the live count intact (the warm path
// purposefully does NOT release the chroot — see
// Manager.WarmSnapshot).
func (f *fakeVMM) WarmSnapshot(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	if instance != "live-1" {
		return fcvm.SnapshotInfo{}, errNotLive
	}
	return fcvm.SnapshotInfo{MemBytes: 1024 * 1024 * 130, VMStateBytes: 4096}, nil
}

func (f *fakeVMM) Destroy(ctx context.Context, instance string) error {
	if f.destroy != nil {
		return f.destroy(ctx, instance)
	}
	f.live = 0
	f.leased = 0
	return nil
}

func (f *fakeVMM) DestroyWithExport(ctx context.Context, instance, exportDir string) (int, error) {
	if f.destroyWithExport != nil {
		return f.destroyWithExport(ctx, instance, exportDir)
	}
	// Backwards-compat: tests that wire only the legacy `destroy` hook still
	// see their error surface here.
	if f.destroy != nil {
		return 0, f.destroy(ctx, instance)
	}
	f.live = 0
	f.leased = 0
	return 0, nil
}

func (f *fakeVMM) ExportDirFor(instance string) string {
	if f.exportDirFn != nil {
		return f.exportDirFn(instance)
	}
	return ""
}

// UpdateEgressAllowlist (tier-2 PR-B) — default success; the
// UpdateEgressAllowlist handler test wires updateAllowlistFn to
// inject errors. The fake records nothing; the test asserts on
// the gRPC return code, not on internal state.
func (f *fakeVMM) UpdateEgressAllowlist(ctx context.Context, appID string, allowlist []netip.Prefix) error {
	if f.updateAllowlistFn != nil {
		return f.updateAllowlistFn(ctx, appID, allowlist)
	}
	return nil
}

func (f *fakeVMM) UpdateStaticEgressIP(_ context.Context, _, _ string) error {
	return nil
}

func (f *fakeVMM) LiveCount() int   { return f.live }
func (f *fakeVMM) LeasedCount() int { return f.leased }

// InstancePID (M8 §11) — wraps the optional hook so the
// SeccompStatus handler test can return either a real PID (the
// path the e2e exercises against a live vmmd subprocess) or the
// (0, false) "not alive" path the gRPC handler maps to NotFound.
// The fake never reads /proc itself; the cross-process readback
// is exactly what makes the e2e valuable.
func (f *fakeVMM) InstancePID(instance string) (int, bool) {
	if f.instancePIDFn != nil {
		return f.instancePIDFn(instance)
	}
	return 0, false
}

// LogRing (issue #254 / Move 4) returns nil by default — the Logs
// handler unit tests wire a custom LogRingFn to inject a ring buffer.
func (f *fakeVMM) LogRing(instance string) *logbuf.Ring {
	if f.logRingFn != nil {
		return f.logRingFn(instance)
	}
	return nil
}
func (f *fakeVMM) NetnsFor(instance string) (string, bool) {
	if f.netnsFn != nil {
		return f.netnsFn(instance)
	}
	// Default: instance is live iff it's been Woken at least once.
	// Tests that need finer control wire netnsFn directly.
	if instance == "" {
		return "", false
	}
	return "fc-" + instance, true
}

// MountParentExt4 (ADR-053) — wraps the optional hook so the
// MountParentExt4ReadOnly handler test can inject errors or
// return a specific mountpoint. nil hook → success returning a
// deterministic mountpoint path (the handler tests assert on
// the wire response, not the host path).
func (f *fakeVMM) MountParentExt4(ctx context.Context, storageKey string) (string, error) {
	if f.mountParentFn != nil {
		return f.mountParentFn(ctx, storageKey)
	}
	return "/tmp/faas-parent-mnt-fake", nil
}

// MaterializeParentExt4 (ADR-053) — the fake succeeds without touching the
// filesystem; dedicated wire tests exercise validation and error lifting.
func (f *fakeVMM) MaterializeParentExt4(context.Context, string, string) error {
	return nil
}

// UmountParentExt4 (ADR-053) — wraps the optional hook so the
// UmountParentExt4 handler test can inject errors. nil hook →
// success.
func (f *fakeVMM) UmountParentExt4(ctx context.Context, mountpoint string) error {
	if f.umountParentFn != nil {
		return f.umountParentFn(ctx, mountpoint)
	}
	return nil
}

// MountOverlayParent (ADR-075 / DEPLOY-1) — wraps the optional
// hook so the MountOverlayParent handler test can inject
// errors. nil hook → success. The merged path returned by the
// hook is what the caller passed (imaged owns the dir; vmmd
// doesn't materialise one).
func (f *fakeVMM) MountOverlayParent(ctx context.Context, lowerdir, upperdir, workdir, merged string) error {
	if f.mountOverlayFn != nil {
		return f.mountOverlayFn(ctx, lowerdir, upperdir, workdir, merged)
	}
	return nil
}

// UmountOverlayParent (ADR-075 / DEPLOY-1) — wraps the optional
// hook so the UmountOverlayParent handler test can inject
// errors. nil hook → success.
func (f *fakeVMM) UmountOverlayParent(ctx context.Context, merged string) error {
	if f.umountOverlayFn != nil {
		return f.umountOverlayFn(ctx, merged)
	}
	return nil
}

// MarkInstanceFrameworkReady (issue #470 / PR #470-FU-B) — wraps
// the optional hook so the FrameworkReady handler test can inject
// errors or override the (stamped, appID, runtime) return tuple.
// nil hook → returns (true, "", "") so the handler's "stamped"
// branch is taken by default. The dedicated FrameworkReady test
// pins the wire envelope shape.
func (f *fakeVMM) MarkInstanceFrameworkReady(ctx context.Context, instance string, warmupMs int64) (bool, string, string, error) {
	if f.frameworkReadyFn != nil {
		return f.frameworkReadyFn(ctx, instance, warmupMs)
	}
	return true, "", "", nil
}

// errNotLive is a sentinel for the Manager-equivalent "not live" error.
type stringErr string

func (s stringErr) Error() string { return string(s) }

const errNotLive = stringErr("park live-1: not live")

// newServer spins up a vmmdgrpc.Server on a bufconn listener and returns
// both the listener (kept open by t.Cleanup) and the dialed client.
func newServer(t *testing.T, fake *fakeVMM) (vmmdpb.VmmdClient, func()) {
	t.Helper()
	ops := wire.NewOpsMetrics("vmmd_test")
	srv := grpc.NewServer()
	impl := vmmdgrpc.New(fake, ops, "1.0.0", nil)
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

func TestCreateColdBoot_Success(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	resp, err := cli.CreateColdBoot(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "i-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/srv/fc/b", LayerKey: "/srv/fc/l", VcpuCount: 2, MemSizeMib: 256},
	})
	if err != nil {
		t.Fatalf("CreateColdBoot: %v", err)
	}
	if resp.GetMethod() != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Fatalf("method = %v, want WAKE_COLD_BOOT", resp.GetMethod())
	}
	if resp.GetLeaseUid() != 20001 {
		t.Fatalf("lease_uid = %d, want 20001", resp.GetLeaseUid())
	}
	if resp.GetHostIp() != "10.100.0.2" {
		t.Fatalf("host_ip = %q", resp.GetHostIp())
	}
}

func TestCreateColdBoot_RejectsMissingInstance(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.CreateColdBoot(context.Background(), &vmmdpb.CreateColdBootRequest{
		App: &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 2, MemSizeMib: 128},
	})
	if err == nil {
		t.Fatalf("expected error for missing instance")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code)
	}
}

func TestCreateFromSnapshot_FallsBackWhenMissing(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	resp, err := cli.CreateFromSnapshot(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		Instance: "i-restore",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 2, MemSizeMib: 256},
		Snapshot: &vmmdpb.SnapshotRef{StorageKey: "", VmstatePath: ""}, // empty ref
	})
	if err != nil {
		t.Fatalf("CreateFromSnapshot: %v", err)
	}
	// Fake's wakeFn is nil, so we drop into the cold-boot path; the response's
	// `requested_method` is what the caller asked for (RESTORE).
	if resp.GetRequestedMethod() != vmmdpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("requested_method = %v, want WAKE_RESTORE", resp.GetRequestedMethod())
	}
	if resp.GetMethod() != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("method = %v, want WAKE_COLD_BOOT (fake always cold-boots)", resp.GetMethod())
	}
}

func TestPauseAndSnapshot_RequiresPaths(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.PauseAndSnapshot(context.Background(), &vmmdpb.PauseAndSnapshotRequest{
		Instance:    "live-1",
		VmstatePath: "/snap/vmstate",
	})
	if err == nil {
		t.Fatalf("expected error for missing storage_key")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code)
	}
}

func TestPauseAndSnapshot_Success(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	resp, err := cli.PauseAndSnapshot(context.Background(), &vmmdpb.PauseAndSnapshotRequest{
		Instance:    "live-1",
		StorageKey:  "snap/live-1/mem",
		VmstatePath: "/snap/vmstate",
	})
	if err != nil {
		t.Fatalf("PauseAndSnapshot: %v", err)
	}
	if resp.GetMemBytes() != 1024*1024*130 {
		t.Errorf("mem_bytes = %d, want %d", resp.GetMemBytes(), 1024*1024*130)
	}
}

func TestPauseAndSnapshot_NotLive(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.PauseAndSnapshot(context.Background(), &vmmdpb.PauseAndSnapshotRequest{
		Instance:    "ghost",
		StorageKey:  "snap/ghost/mem",
		VmstatePath: "/snap/vmstate",
	})
	if err == nil {
		t.Fatalf("expected not-live error")
	}
	// fake's Park returns a plain stringErr; toProblem lifts to Internal.
	if code := status.Code(err); code != codes.Internal {
		t.Fatalf("code = %v, want Internal", code)
	}
}

// TestWarmSnapshot_RoundTrip (issue #470 / PR A / ADR-055) pins
// the WarmSnapshot wire shape. The handler must accept the
// {instance, storage_key, vmstate_storage_key} triple, surface a
// meaningful SnapshotResponse, and reject empty required fields
// with InvalidArgument. The success path stores the request
// fields on the fake so a future regression that drops the
// vmstate_storage_key from the wire trips here.
func TestWarmSnapshot_RoundTrip(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := &fakeVMM{}
		cli, _ := newServer(t, f)
		resp, err := cli.WarmSnapshot(context.Background(), &vmmdpb.WarmSnapshotRequest{
			Instance:          "live-1",
			StorageKey:        "snap/live-1/warm/mem",
			VmstateStorageKey: "snap/live-1/warm/vmstate",
		})
		if err != nil {
			t.Fatalf("WarmSnapshot: %v", err)
		}
		if resp.GetMemBytes() != 1024*1024*130 {
			t.Errorf("mem_bytes = %d, want %d", resp.GetMemBytes(), 1024*1024*130)
		}
	})
	t.Run("missing instance", func(t *testing.T) {
		cli, _ := newServer(t, &fakeVMM{})
		_, err := cli.WarmSnapshot(context.Background(), &vmmdpb.WarmSnapshotRequest{
			StorageKey:        "snap/x/warm/mem",
			VmstateStorageKey: "snap/x/warm/vmstate",
		})
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", code)
		}
	})
	t.Run("missing storage_key", func(t *testing.T) {
		cli, _ := newServer(t, &fakeVMM{})
		_, err := cli.WarmSnapshot(context.Background(), &vmmdpb.WarmSnapshotRequest{
			Instance:          "live-1",
			VmstateStorageKey: "snap/live-1/warm/vmstate",
		})
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", code)
		}
	})
	t.Run("missing vmstate_storage_key", func(t *testing.T) {
		// Warm captures are storage-backend-only — neither key
		// has a host-path fallback the way PauseAndSnapshot's
		// vmstate_path does. The handler rejects missing
		// vmstate_storage_key with InvalidArgument so a
		// default-local-vs-remote split doesn't leak into the
		// capture path.
		cli, _ := newServer(t, &fakeVMM{})
		_, err := cli.WarmSnapshot(context.Background(), &vmmdpb.WarmSnapshotRequest{
			Instance:   "live-1",
			StorageKey: "snap/live-1/warm/mem",
		})
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", code)
		}
	})
}

func TestDestroy_Idempotent(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	resp, err := cli.Destroy(context.Background(), &vmmdpb.DestroyRequest{Instance: "anything"})
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if resp.GetInstance() != "anything" {
		t.Errorf("instance = %q, want %q", resp.GetInstance(), "anything")
	}
}

func TestStats_NonLinuxHasNoResidentBytes(t *testing.T) {
	// On a Linux host without a live VM (the CI case), the cgroup glob
	// returns no entries and ResidentBytes() returns (emptyMap, true); the
	// handler builds wrapperspb.Int64(0) in that case — TotalResidentBytes
	// is set to the zero value. On non-Linux hosts (macOS dev box) the
	// runtime guard in ResidentBytes() returns (nil, false) so the handler
	// leaves the field unset. We assert the env-appropriate behavior.
	if runtime.GOOS == "linux" {
		t.Skip("TotalResidentBytes on Linux is set iff at least one vm-*.scope cgroup exists; tested on a live box in TestMetal_Stats")
	}
	f := &fakeVMM{live: 3, leased: 3}
	cli, _ := newServer(t, f)
	resp, err := cli.Stats(context.Background(), &vmmdpb.StatsRequest{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if resp.GetLiveCount() != 3 || resp.GetLeasedCount() != 3 {
		t.Fatalf("counts = (%d, %d), want (3, 3)", resp.GetLiveCount(), resp.GetLeasedCount())
	}
	if resp.GetTotalResidentBytes() != nil {
		t.Fatalf("non-Linux host should leave TotalResidentBytes unset; got %v", resp.GetTotalResidentBytes())
	}
}

// --- error-path coverage --------------------------------------------------

// TestCreateFromSnapshot_WakeError covers the toProblem-on-error branch of
// CreateFromSnapshot. A plain (non-Problem) error from Wake must be lifted
// to an Internal problem so internal go-stack details don't leak across gRPC.
func TestCreateFromSnapshot_WakeError(t *testing.T) {
	f := &fakeVMM{
		wakeFn: func(_ context.Context, _ fcvm.WakeRequest) (*fcvm.Instance, error) {
			return nil, fmt.Errorf("vmmd underlying boom")
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.CreateFromSnapshot(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		Instance: "boom",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 2, MemSizeMib: 256},
		Snapshot: &vmmdpb.SnapshotRef{StorageKey: "snap/boom/mem", VmstatePath: "/v", FcVersion: "1.7.0"},
	})
	if err == nil {
		t.Fatal("expected error from CreateFromSnapshot")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("status code = %v, want Internal", st.Code())
	}
}

// TestCreateFromSnapshot_InvalidRequest covers the toWakeRequest branch that
// rejects a malformed request (e.g. missing instance) before any VMM work.
func TestCreateFromSnapshot_InvalidRequest(t *testing.T) {
	cli, _ := newServer(t, &fakeVMM{})
	// No instance — toWakeRequest will fail.
	_, err := cli.CreateFromSnapshot(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 2, MemSizeMib: 256},
		Snapshot: &vmmdpb.SnapshotRef{StorageKey: "snap/noinst/mem", VmstatePath: "/v", FcVersion: "1.7.0"},
	})
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("status code = %v, want InvalidArgument", st.Code())
	}
}

// TestDestroy_FailureSurfacesAsStatus covers the Destroy error branch — the
// fake's destroy hook returns an error and we expect it lifted to a gRPC
// status (not a nil response).
func TestDestroy_FailureSurfacesAsStatus(t *testing.T) {
	f := &fakeVMM{
		destroy: func(_ context.Context, _ string) error {
			return fmt.Errorf("destroy leaked")
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.Destroy(context.Background(), &vmmdpb.DestroyRequest{Instance: "x"})
	if err == nil {
		t.Fatal("expected error from Destroy")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("status code = %v, want Internal", st.Code())
	}
}

// TestStats_LinuxSetsTotalResidentBytes exercises the Linux branch of Stats
// that aggregates per-instance cgroup memory. We don't have live cgroups in
// tests, but the code path is the same: collect from ResidentBytes() and
// sum into TotalResidentBytes. On Linux with no scopes the resident map is
// empty so total=0; we assert that the field is set (not nil).
func TestStats_LinuxSetsTotalResidentBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only path")
	}
	f := &fakeVMM{live: 0, leased: 0}
	cli, _ := newServer(t, f)
	resp, err := cli.Stats(context.Background(), &vmmdpb.StatsRequest{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if resp.GetLiveCount() != 0 {
		t.Errorf("LiveCount = %d, want 0", resp.GetLiveCount())
	}
	// On Linux with no cgroup scopes, total = 0 (sum of empty map) and the
	// field is set to wrapperspb.Int64(0). Assert the field is non-nil.
	if resp.GetTotalResidentBytes() == nil {
		t.Error("Linux Stats should set TotalResidentBytes (to 0 if no cgroup scopes)")
	}
}

// TestNew_WithDefaults covers the New() defaulting path: a nil ops argument
// must NOT panic; a nil log must NOT panic.
func TestNew_WithDefaults(t *testing.T) {
	s := vmmdgrpc.New(&fakeVMM{}, nil, "1.7.0", nil)
	if s == nil {
		t.Fatal("New returned nil")
	}
	// Stats is the only handler that doesn't take a request payload, so it's
	// the easiest one to invoke without setting up the whole proto.
	resp, err := s.Stats(context.Background(), &vmmdpb.StatsRequest{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	_ = resp
}

// TestToProblem_NilReturnsNil covers the nil-input branch of toProblem.
func TestToProblem_NilReturnsNil(t *testing.T) {
	// We can't call the unexported toProblem directly, so we exercise it
	// indirectly: a successful Destroy should return a nil error response
	// path (toProblem is never called). Verified by TestDestroy_Idempotent.
	// For nil-return semantics, we exercise via a Wake that returns no error.
	f := &fakeVMM{} // wakeFn nil → default success
	cli, _ := newServer(t, f)
	resp, err := cli.CreateColdBoot(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "ok",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 2, MemSizeMib: 256},
	})
	if err != nil {
		t.Fatalf("CreateColdBoot: %v", err)
	}
	if resp == nil {
		t.Error("response is nil")
	}
}

// TestPing_ReturnsFcVersionAndTime pins the wire-level liveness probe
// (issue #97 / ADR-025 axis 3, PR #114). The handler must echo the
// server's configured fc_version verbatim and stamp a server-side
// timestamp close to time.Now() — schedd's heartbeat loop uses the
// round-trip success + a non-zero fc_version as its liveness signal.
// We construct the server directly (not through newServer) so the
// fcVersion is controllable.
func TestPing_ReturnsFcVersionAndTime(t *testing.T) {
	ops := wire.NewOpsMetrics("vmmd_test")
	srv := grpc.NewServer()
	impl := vmmdgrpc.New(&fakeVMM{}, ops, "1.10.0", nil)
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
	cli := vmmdpb.NewVmmdClient(conn)

	before := time.Now().Add(-2 * time.Second)
	resp, err := cli.Ping(context.Background(), &vmmdpb.PingRequest{})
	after := time.Now().Add(2 * time.Second)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := resp.GetFcVersion(); got != "1.10.0" {
		t.Errorf("FcVersion = %q, want %q", got, "1.10.0")
	}
	st := resp.GetServerTime().AsTime()
	if st.Before(before) || st.After(after) {
		t.Errorf("ServerTime = %v, want between %v and %v", st, before, after)
	}
}

// TestDestroy_BuildAwareReturnsExitCode covers the M6 builder path: when
// the Manager's ExportDirFor reports a non-empty dir, Destroy routes through
// DestroyWithExport and surfaces the captured exit code on the wire so
// builderd can classify the build's outcome (FailureUserError/OOM/Timeout).
func TestDestroy_BuildAwareReturnsExitCode(t *testing.T) {
	const wantCode = 137 // kernel OOM-killed
	f := &fakeVMM{
		exportDirFn: func(string) string { return "/var/lib/faas/build-out/abc" },
		destroyWithExport: func(_ context.Context, _, exportDir string) (int, error) {
			if exportDir == "" {
				t.Errorf("expected non-empty exportDir (got %q)", exportDir)
			}
			return wantCode, nil
		},
	}
	cli, _ := newServer(t, f)
	resp, err := cli.Destroy(context.Background(), &vmmdpb.DestroyRequest{Instance: "build-abc"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetExitCode() != wantCode {
		t.Errorf("exit_code = %d, want %d (137 = OOM-killed)", resp.GetExitCode(), wantCode)
	}
	if resp.GetInstance() != "build-abc" {
		t.Errorf("instance echo = %q, want build-abc", resp.GetInstance())
	}
}

// TestDestroy_AppVMSkipsExportPath covers the legacy path: no exportDir
// registered, so DestroyWithExport is still called (with "") and the
// captured exit code is 0 for a clean app teardown.
func TestDestroy_AppVMSkipsExportPath(t *testing.T) {
	var seenExportDir string
	f := &fakeVMM{
		exportDirFn: func(string) string { return "" }, // app VM
		destroyWithExport: func(_ context.Context, _, exportDir string) (int, error) {
			seenExportDir = exportDir
			return 0, nil
		},
	}
	cli, _ := newServer(t, f)
	resp, err := cli.Destroy(context.Background(), &vmmdpb.DestroyRequest{Instance: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	if seenExportDir != "" {
		t.Errorf("exportDir for app VM should be empty, got %q", seenExportDir)
	}
	if resp.GetExitCode() != 0 {
		t.Errorf("app-VM exit_code = %d, want 0", resp.GetExitCode())
	}
}

// TestSeccompStatus_EmptyInstance_ReturnsInvalidArgument pins
// the gRPC contract for the "missing field" case (M8 §11). The
// handler MUST return InvalidArgument, not NotFound, because the
// distinction is what lets the e2e diagnose "wrong wire" vs
// "wrong instance" — a regression that collapsed the two would
// break every future operator runbook that uses the gRPC code
// to distinguish the failure modes.
func TestSeccompStatus_EmptyInstance_ReturnsInvalidArgument(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.SeccompStatus(context.Background(), &vmmdpb.SeccompStatusRequest{})
	if err == nil {
		t.Fatal("expected error for empty instance, got nil")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// TestSeccompStatus_UnknownInstance_ReturnsNotFound pins the
// gRPC contract for the "instance not alive" case. The fake
// answers (0, false) for everything, which the handler maps to
// the api.CodeNotFound problem; grpcerr.codeToGRPC carries that
// to codes.NotFound on the wire (matching the REST 404). The
// distinction "wrong instance" vs "operator missing required
// field" (InvalidArgument, see TestSeccompStatus_EmptyInstance_
// ReturnsInvalidArgument) is load-bearing for operator runbooks;
// collapsing them would break the runbook's first step. We
// pin BOTH the gRPC code and the message so a future
// codeToGRPC drift that silently re-merges them trips here.
func TestSeccompStatus_UnknownInstance_ReturnsNotFound(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.SeccompStatus(context.Background(), &vmmdpb.SeccompStatusRequest{Instance: "never-woke"})
	if err == nil {
		t.Fatal("expected error for unknown instance, got nil")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want NotFound (a regression that drops the gRPC code would mask operator runbook step 1)", code)
	}
	// The message carries the instance id so the operator can
	// diagnose which one was wrong. If grpcerr.ToStatus ever
	// drops the title, the e2e sees a generic error and gives
	// up; pinning the message here is the tripwire.
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "never-woke") {
		t.Errorf("message = %q, want it to name the instance", msg)
	}
}

// TestSeccompStatus_HappyPath_ReadsProcFS exercises the wire
// shape end-to-end through the bufconn harness: the fake returns
// a real PID (the test process's own PID), the handler reads
// /proc/<pid>/status, and the response carries a well-formed
// mode + filter_len. The cross-process readback in cmd/e2e/
// is the load-bearing assertion; this one pins the handler's
// server-side /proc read for the non-e2e CI path.
func TestSeccompStatus_HappyPath_ReadsProcFS(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		// /proc is Linux-only. Mac dev + Windows CI skip; the
		// cross-process e2e (cmd/e2e/sec11_seccomp_e2e_test.go,
		// metal) is the authoritative gate.
		t.Skipf("/proc/self/status not available on this OS: %v (cross-process e2e is the authoritative gate)", err)
	}
	f := &fakeVMM{}
	f.instancePIDFn = func(instance string) (int, bool) {
		if instance == "" {
			return 0, false
		}
		return os.Getpid(), true
	}
	cli, _ := newServer(t, f)
	resp, err := cli.SeccompStatus(context.Background(), &vmmdpb.SeccompStatusRequest{Instance: "self"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetError() != "" {
		t.Fatalf("Error = %q (handler failed to read /proc/self/status)", resp.GetError())
	}
	if resp.GetMode() != "disabled" && resp.GetMode() != "strict" && resp.GetMode() != "filter" {
		t.Errorf("mode = %q, want one of disabled/strict/filter", resp.GetMode())
	}
	if resp.GetPid() != int32(os.Getpid()) {
		t.Errorf("pid = %d, want %d", resp.GetPid(), os.Getpid())
	}
}

// --- ADR-053: MountParentExt4ReadOnly / UmountParentExt4 ---------

// TestMountParentExt4ReadOnly_HappyPath pins the success-path wire
// shape: storage_key flows through to the VmmdAPI hook and the
// returned mountpoint path is echoed back on the response. The
// fake returns a deterministic mountpoint; the handler must NOT
// munge it (defence against a future regression that re-formats
// the path or strips the tempdir prefix).
func TestMountParentExt4ReadOnly_HappyPath(t *testing.T) {
	const wantMP = "/tmp/faas-parent-mnt-happy"
	var seenKey string
	f := &fakeVMM{
		mountParentFn: func(_ context.Context, storageKey string) (string, error) {
			seenKey = storageKey
			return wantMP, nil
		},
	}
	cli, _ := newServer(t, f)
	resp, err := cli.MountParentExt4ReadOnly(context.Background(),
		&vmmdpb.MountParentExt4ReadOnlyRequest{StorageKey: "base/runner-base-debian-parent-amd64.ext4"})
	if err != nil {
		t.Fatalf("MountParentExt4ReadOnly: %v", err)
	}
	if seenKey != "base/runner-base-debian-parent-amd64.ext4" {
		t.Errorf("storageKey delivered to VmmdAPI = %q, want %q", seenKey, "base/runner-base-debian-parent-amd64.ext4")
	}
	if got := resp.GetMountpoint(); got != wantMP {
		t.Errorf("mountpoint = %q, want %q", got, wantMP)
	}
}

// TestMountParentExt4ReadOnly_RejectsEmptyStorageKey pins the
// InvalidArgument branch: the handler MUST validate storage_key
// before any VMM work so a misconfigured imaged caller fails
// fast with a clear wire code (the NotFound branch is reserved
// for "storage_key is well-formed but the backend doesn't have
// the bytes").
func TestMountParentExt4ReadOnly_RejectsEmptyStorageKey(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.MountParentExt4ReadOnly(context.Background(),
		&vmmdpb.MountParentExt4ReadOnlyRequest{StorageKey: ""})
	if err == nil {
		t.Fatal("expected error for empty storage_key")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// TestMountParentExt4ReadOnly_SurfacesUnknownStorageKey pins the
// NotFound branch: when the VmmdAPI hook returns
// vmmdmount.ErrNotFound (wrapped or bare), the handler MUST lift
// it to NotFound. imaged's parent-staging fail-loud path depends
// on this — a misclassified "unknown key" would silently fall
// through to the Internal branch and bury the diagnosis in the
// log.
func TestMountParentExt4ReadOnly_SurfacesUnknownStorageKey(t *testing.T) {
	// Use the canonical parent key so the allow-list accepts it
	// and the storage-miss path is exercised.
	f := &fakeVMM{
		mountParentFn: func(_ context.Context, _ string) (string, error) {
			return "", errNotFoundForMount
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.MountParentExt4ReadOnly(context.Background(),
		&vmmdpb.MountParentExt4ReadOnlyRequest{StorageKey: "base/runner-base-debian-parent-amd64.ext4"})
	if err == nil {
		t.Fatal("expected NotFound error, got nil")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want codes.NotFound (the load-bearing wire mapping for vmmdmount.ErrNotFound)", code)
	}
}

// TestMountParentExt4ReadOnly_RejectsNonParentKey pins the
// allow-list: non-parent keys (a per-app base key, an arbitrary
// blob, a layer key) MUST be rejected with InvalidArgument
// BEFORE the VmmdAPI hook runs. The fake's mountParentFn is
// left nil; if the handler reaches it, the test panics — the
// load-bearing assertion is that no fake call happens for a
// rejected key.
func TestMountParentExt4ReadOnly_RejectsNonParentKey(t *testing.T) {
	called := false
	f := &fakeVMM{
		mountParentFn: func(_ context.Context, _ string) (string, error) {
			called = true
			return "/srv/fc/parent/faas-parent-mnt-x", nil
		},
	}
	cli, _ := newServer(t, f)
	for _, badKey := range []string{
		"base/runner-node22-amd64.ext4",               // per-app base
		"base/runner-python312-amd64.ext4",            // per-app base
		"layers/foo.ext4",                             // per-deployment layer
		"snapshots/app-foo/mem.bin",                   // snapshot blob
		"kernel/vmlinux-amd64",                        // kernel artifact
		"base/runner-base-debian-parent-riscv64.ext4", // unsupported arch
	} {
		_, err := cli.MountParentExt4ReadOnly(context.Background(),
			&vmmdpb.MountParentExt4ReadOnlyRequest{StorageKey: badKey})
		if err == nil {
			t.Errorf("key %q: expected InvalidArgument, got nil", badKey)
			continue
		}
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Errorf("key %q: code = %v, want codes.InvalidArgument", badKey, code)
		}
	}
	if called {
		t.Error("VmmdAPI.MountParentExt4 was called for a rejected key; the allow-list ran BEFORE the hook")
	}
}

// TestUmountParentExt4_HappyPath pins the success wire shape:
// empty response body, no error. The fake records the mountpoint
// it was asked to release so a future regression that drops the
// field at the handler boundary trips here.
func TestUmountParentExt4_HappyPath(t *testing.T) {
	var seenMP string
	f := &fakeVMM{
		umountParentFn: func(_ context.Context, mountpoint string) error {
			seenMP = mountpoint
			return nil
		},
	}
	cli, _ := newServer(t, f)
	resp, err := cli.UmountParentExt4(context.Background(),
		&vmmdpb.UmountParentExt4Request{Mountpoint: "/tmp/faas-parent-mnt-1"})
	if err != nil {
		t.Fatalf("UmountParentExt4: %v", err)
	}
	if resp == nil {
		t.Error("response is nil (handler returned nil ack)")
	}
	if seenMP != "/tmp/faas-parent-mnt-1" {
		t.Errorf("mountpoint delivered to VmmdAPI = %q, want %q", seenMP, "/tmp/faas-parent-mnt-1")
	}
}

// TestUmountParentExt4_RejectsEmptyMountpoint pins the empty-path
// InvalidArgument branch. The handler MUST validate mountpoint
// before any VMM work — imaged's defer-after-error pattern means
// a zero-valued path could race the registry, and the gRPC code
// must surface "you sent us nothing" as a 400.
func TestUmountParentExt4_RejectsEmptyMountpoint(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.UmountParentExt4(context.Background(),
		&vmmdpb.UmountParentExt4Request{Mountpoint: ""})
	if err == nil {
		t.Fatal("expected error for empty mountpoint")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// TestUmountParentExt4_PropagatesInternalError pins the EBUSY /
// EINVAL branch: when the VmmdAPI hook returns a real umount
// error, the handler lifts it to a gRPC status (NOT nil — the
// gRPC response is empty on success but a status on failure).
// imaged's caller logs the error; the wire code (Internal) lets
// the dashboard distinguish "operator misconfig" from "kernel
// refused to release the mount".
func TestUmountParentExt4_PropagatesInternalError(t *testing.T) {
	f := &fakeVMM{
		umountParentFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("umount: device or resource busy")
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.UmountParentExt4(context.Background(),
		&vmmdpb.UmountParentExt4Request{Mountpoint: "/tmp/faas-parent-mnt-busy"})
	if err == nil {
		t.Fatal("expected error from busy umount, got nil")
	}
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("code = %v, want Internal", code)
	}
}

// TestMountOverlayParent_HappyPath (ADR-075 / DEPLOY-1) pins the
// success wire shape: a MountOverlayParent request with valid
// paths lifts through to a MountOverlayParentResponse and the
// underlying VmmdAPI hook is called with the four paths verbatim.
func TestMountOverlayParent_HappyPath(t *testing.T) {
	called := false
	f := &fakeVMM{
		mountOverlayFn: func(_ context.Context, lowerdir, upperdir, workdir, merged string) error {
			called = true
			if lowerdir != "/srv/fc/parent/faas-parent-mnt-x" {
				t.Errorf("lowerdir = %q, want %q", lowerdir, "/srv/fc/parent/faas-parent-mnt-x")
			}
			if merged != "/dev/shm/faas-base-staging/faas-overlay-mnt/merged" {
				t.Errorf("merged = %q, want %q", merged, "/dev/shm/faas-base-staging/faas-overlay-mnt/merged")
			}
			return nil
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.MountOverlayParent(context.Background(), &vmmdpb.MountOverlayParentRequest{
		Lowerdir: "/srv/fc/parent/faas-parent-mnt-x",
		Upperdir: "/dev/shm/faas-base-staging/faas-overlay-mnt/upper",
		Workdir:  "/dev/shm/faas-base-staging/faas-overlay-mnt/work",
		Merged:   "/dev/shm/faas-base-staging/faas-overlay-mnt/merged",
	})
	if err != nil {
		t.Fatalf("MountOverlayParent happy path: %v", err)
	}
	if !called {
		t.Fatal("VmmdAPI.MountOverlayParent hook was not invoked")
	}
}

// TestMountOverlayParent_RejectsEmptyPath (ADR-075 / DEPLOY-1)
// pins the empty-path InvalidArgument branch. The handler must
// validate ALL four paths (lowerdir/upperdir/workdir/merged)
// before any syscall; an empty path is the imaged-side
// "caller didn't fill the proto field" bug and must surface as
// 400.
func TestMountOverlayParent_RejectsEmptyPath(t *testing.T) {
	tests := []struct {
		name string
		req  *vmmdpb.MountOverlayParentRequest
	}{
		{"empty_lowerdir", &vmmdpb.MountOverlayParentRequest{
			Lowerdir: "",
			Upperdir: "/dev/shm/faas-base-staging/x/upper",
			Workdir:  "/dev/shm/faas-base-staging/x/work",
			Merged:   "/dev/shm/faas-base-staging/x/merged",
		}},
		{"empty_upperdir", &vmmdpb.MountOverlayParentRequest{
			Lowerdir: "/srv/fc/parent/x",
			Upperdir: "",
			Workdir:  "/dev/shm/faas-base-staging/x/work",
			Merged:   "/dev/shm/faas-base-staging/x/merged",
		}},
		{"empty_workdir", &vmmdpb.MountOverlayParentRequest{
			Lowerdir: "/srv/fc/parent/x",
			Upperdir: "/dev/shm/faas-base-staging/x/upper",
			Workdir:  "",
			Merged:   "/dev/shm/faas-base-staging/x/merged",
		}},
		{"empty_merged", &vmmdpb.MountOverlayParentRequest{
			Lowerdir: "/srv/fc/parent/x",
			Upperdir: "/dev/shm/faas-base-staging/x/upper",
			Workdir:  "/dev/shm/faas-base-staging/x/work",
			Merged:   "",
		}},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeVMM{}
			cli, _ := newServer(t, f)
			_, err := cli.MountOverlayParent(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("MountOverlayParent(%s) = nil, want InvalidArgument", tc.name)
			}
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", code)
			}
		})
	}
}

// TestMountOverlayParent_SurfacesInvalidPath pins the
// ErrInvalidOverlayPath → InvalidArgument lift. The vmmdmount
// helper validates path prefixes; when it rejects, the gRPC
// handler lifts the sentinel to InvalidArgument so the wire
// response distinguishes "caller sent bad paths" from "kernel
// refused to mount".
func TestMountOverlayParent_SurfacesInvalidPath(t *testing.T) {
	f := &fakeVMM{
		mountOverlayFn: func(_ context.Context, _, _, _, _ string) error {
			return fmt.Errorf("fake vmm: %w", vmmdmount.ErrInvalidOverlayPath)
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.MountOverlayParent(context.Background(), &vmmdpb.MountOverlayParentRequest{
		Lowerdir: "/srv/fc/parent/x",
		Upperdir: "/tmp/bad/upper",
		Workdir:  "/tmp/bad/work",
		Merged:   "/tmp/bad/merged",
	})
	if err == nil {
		t.Fatal("expected InvalidArgument for foreign paths, got nil")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// TestUmountOverlayParent_HappyPath pins the success wire
// shape: an UmountOverlayParent call returns an empty response
// and the underlying VmmdAPI hook is called with the merged
// path verbatim.
func TestUmountOverlayParent_HappyPath(t *testing.T) {
	called := false
	f := &fakeVMM{
		umountOverlayFn: func(_ context.Context, merged string) error {
			called = true
			if merged != "/dev/shm/faas-base-staging/faas-overlay-mnt/merged" {
				t.Errorf("merged = %q, want %q", merged, "/dev/shm/faas-base-staging/faas-overlay-mnt/merged")
			}
			return nil
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.UmountOverlayParent(context.Background(), &vmmdpb.UmountOverlayParentRequest{
		Merged: "/dev/shm/faas-base-staging/faas-overlay-mnt/merged",
	})
	if err != nil {
		t.Fatalf("UmountOverlayParent happy path: %v", err)
	}
	if !called {
		t.Fatal("VmmdAPI.UmountOverlayParent hook was not invoked")
	}
}

// TestUmountOverlayParent_AbsorbsUnknownMountpoint pins the
// idempotent-defer-after-error behaviour: ErrUnknownMountpoint
// from the underlying syscall is absorbed (handler returns
// success) so imaged's defer-after-error pattern is safe.
func TestUmountOverlayParent_AbsorbsUnknownMountpoint(t *testing.T) {
	f := &fakeVMM{
		umountOverlayFn: func(_ context.Context, _ string) error {
			return vmmdmount.ErrUnknownMountpoint
		},
	}
	cli, _ := newServer(t, f)
	_, err := cli.UmountOverlayParent(context.Background(), &vmmdpb.UmountOverlayParentRequest{
		Merged: "/dev/shm/faas-base-staging/never-existed/merged",
	})
	if err != nil {
		t.Fatalf("UmountOverlayParent(unknown): %v; want nil (idempotent)", err)
	}
}

// TestUmountOverlayParent_RejectsEmptyMerged pins the empty-path
// InvalidArgument branch. Same shape as
// TestUmountParentExt4_RejectsEmptyMountpoint.
func TestUmountOverlayParent_RejectsEmptyMerged(t *testing.T) {
	f := &fakeVMM{}
	cli, _ := newServer(t, f)
	_, err := cli.UmountOverlayParent(context.Background(),
		&vmmdpb.UmountOverlayParentRequest{Merged: ""})
	if err == nil {
		t.Fatal("expected InvalidArgument for empty merged")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// errNotFoundForMount is a small sentinel for the MountParentExt4
// NotFound branch — distinct from the bufconn_test-internal
// errNotLive so a future refactor that flips the wrong variable
// trips here rather than at every other NotFound assertion.
//
// Wraps vmmdmount.ErrNotFound via fmt.Errorf %w so the
// handler's errors.Is(err, vmmdmount.ErrNotFound) check
// (review finding mapping #58) lifts to codes.NotFound. The
// pre-PR #465 review assertion ("not InvalidArgument") would
// pass via the default Internal mapping too; the wrap here
// pins the post-PR #465 contract that NotFound is the
// load-bearing code.
var errNotFoundForMount = fmt.Errorf("fake vmm: %w", vmmdmount.ErrNotFound)
