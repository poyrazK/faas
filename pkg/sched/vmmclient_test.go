// vmmclient_test.go — exercises the typed vmmd wrapper end-to-end against the
// real pkg/vmmdgrpc.Server over bufconn, mirroring pkg/vmmdgrpc/bufconn_test.go.
// A fake VmmdAPI stands in for firecracker so the wire path (proto round-trip +
// error re-lifting) is fully covered without KVM.

package sched_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
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

func (f *fakeVMM) Destroy(ctx context.Context, instance string) error {
	if f.destFn != nil {
		return f.destFn(ctx, instance)
	}
	return nil
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

func (f *fakeVMM) ExportDirFor(instance string) string { return "" }

func (f *fakeVMM) LiveCount() int   { return 0 }
func (f *fakeVMM) LeasedCount() int { return 0 }

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
		BasePath: "/srv/fc/base", LayerPath: "/srv/fc/layer", VCPUCount: 2, MemSizeMiB: 256,
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
		sched.AppSpec{BasePath: "/b", LayerPath: "/l", VCPUCount: 2, MemSizeMiB: 256},
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
	b, err := c.PauseAndSnapshot(context.Background(), "i-1", "/snap/vmstate", "snap/i-1/mem")
	if err != nil {
		t.Fatalf("PauseAndSnapshot: %v", err)
	}
	if b.MemBytes != 130*1024*1024 {
		t.Errorf("mem_bytes = %d", b.MemBytes)
	}
}

func TestVMMClient_PauseAndSnapshot_MissingPaths(t *testing.T) {
	c := newClient(t, &fakeVMM{})
	_, err := c.PauseAndSnapshot(context.Background(), "i-1", "/snap/vmstate", "")
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
		sched.AppSpec{BasePath: "/b", LayerPath: "/l", VCPUCount: 2, MemSizeMiB: 256})
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
