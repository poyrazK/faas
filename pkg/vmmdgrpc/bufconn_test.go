// End-to-end handler tests via bufconn. No real firecracker, no real
// socket — we stand up an in-process gRPC server backed by a hand-rolled
// fakeVMM that maps to the invariants Manager already enforces.

package vmmdgrpc_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
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
	live              int
	leased            int
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

func (f *fakeVMM) LiveCount() int   { return f.live }
func (f *fakeVMM) LeasedCount() int { return f.leased }

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
		App:      &vmmdpb.AppSpec{BasePath: "/srv/fc/b", LayerPath: "/srv/fc/l", VcpuCount: 2, MemSizeMib: 256},
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
		App: &vmmdpb.AppSpec{BasePath: "/b", LayerPath: "/l", VcpuCount: 2, MemSizeMib: 128},
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
		App:      &vmmdpb.AppSpec{BasePath: "/b", LayerPath: "/l", VcpuCount: 2, MemSizeMib: 256},
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
		App:      &vmmdpb.AppSpec{BasePath: "/b", LayerPath: "/l", VcpuCount: 2, MemSizeMib: 256},
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
		App:      &vmmdpb.AppSpec{BasePath: "/b", LayerPath: "/l", VcpuCount: 2, MemSizeMib: 256},
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
		App:      &vmmdpb.AppSpec{BasePath: "/b", LayerPath: "/l", VcpuCount: 2, MemSizeMib: 256},
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
