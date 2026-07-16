// End-to-end handler tests via bufconn. No real firecracker, no real
// socket — we stand up an in-process gRPC server backed by a hand-rolled
// fakeVMM that maps to the invariants Manager already enforces.

package vmmdgrpc_test

import (
	"context"
	"net"
	"net/netip"
	"testing"

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
	wakeFn  func(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error)
	parkFn  func(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
	destroy func(ctx context.Context, instance string) error
	live    int
	leased  int
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
		Snapshot: &vmmdpb.SnapshotRef{MemPath: "", VmstatePath: ""}, // empty ref
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
		MemPath:     "",
		VmstatePath: "/snap/vmstate",
	})
	if err == nil {
		t.Fatalf("expected error for missing mem_path")
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
		MemPath:     "/snap/mem",
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
		MemPath:     "/snap/mem",
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
