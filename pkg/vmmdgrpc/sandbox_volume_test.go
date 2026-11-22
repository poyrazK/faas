package vmmdgrpc_test

// adr: 20261122132055 §4 — vmmd gives a persistent session its volume only
// within the plan and only when it keeps volumes, and deletes one only when
// no running session uses it.

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

func newVolumeClient(t *testing.T, vmm vmmdgrpc.VmmdAPI, volumes *fcvm.SandboxVolumes) vmmdpb.VmmdClient {
	t.Helper()
	srv := grpc.NewServer()
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_sandbox_volume_test"), "1.0.0", nil).WithAgentRuns(true)
	if volumes != nil {
		s.WithSandboxVolumes(volumes)
	}
	s.Register(srv)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

// startWithVolume opens a stream with the start frame changed by edit and
// returns the stream's first answer's code.
func startWithVolume(t *testing.T, cli vmmdpb.VmmdClient, edit func(*vmmdpb.RunSandboxStart)) codes.Code {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := cli.RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := sandboxRunStart()
	edit(start.GetStart())
	if err := stream.Send(start); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	return status.Code(err)
}

func TestRunSandboxVolumeIsCheckedBeforeTheBoot(t *testing.T) {
	vmm := &sandboxRunVMM{}
	cli := newVolumeClient(t, vmm, fcvm.NewSandboxVolumes(t.TempDir()))
	for _, tc := range []struct {
		name string
		edit func(*vmmdpb.RunSandboxStart)
		want codes.Code
	}{
		{"past the plan", func(s *vmmdpb.RunSandboxStart) { s.VolumeGb = 51 }, codes.InvalidArgument},
		{"a backup without a volume", func(s *vmmdpb.RunSandboxStart) { s.VolumeBackupId = "x" }, codes.InvalidArgument},
		{"a backup id that is no UUID", func(s *vmmdpb.RunSandboxStart) { s.VolumeGb, s.VolumeBackupId = 10, "x" }, codes.InvalidArgument},
		{"a task with a volume", func(s *vmmdpb.RunSandboxStart) {
			s.VolumeGb, s.Task = 10, &vmmdpb.RunSandboxTask{Kind: "browse", Url: "https://example.com"}
		}, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startWithVolume(t, cli, tc.edit); got != tc.want {
				t.Fatalf("code = %s, want %s", got, tc.want)
			}
		})
	}
	if woken, _, _, _ := vmm.state(); woken != 0 {
		t.Fatalf("%d VMs booted for refused starts", woken)
	}
	// A vmmd that keeps no volumes refuses a session with one.
	bare := newVolumeClient(t, &sandboxRunVMM{}, nil)
	if got := startWithVolume(t, bare, func(s *vmmdpb.RunSandboxStart) { s.VolumeGb = 10 }); got != codes.Unimplemented {
		t.Fatalf("no volumes = %s", got)
	}
}

func TestDeleteSandboxVolume(t *testing.T) {
	const account, sandbox = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	ctx := context.Background()
	cli := newVolumeClient(t, &sandboxRunVMM{}, fcvm.NewSandboxVolumes(t.TempDir()))
	if _, err := cli.DeleteSandboxVolume(ctx, &vmmdpb.DeleteSandboxVolumeRequest{AccountId: "../x", SandboxId: sandbox}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad id = %v", err)
	}
	resp, err := cli.DeleteSandboxVolume(ctx, &vmmdpb.DeleteSandboxVolumeRequest{AccountId: account, SandboxId: sandbox})
	if err != nil || resp.GetDeleted() {
		t.Fatalf("a volume the node never had = %v %v", resp, err)
	}
	bare := newVolumeClient(t, &sandboxRunVMM{}, nil)
	if _, err := bare.DeleteSandboxVolume(ctx, &vmmdpb.DeleteSandboxVolumeRequest{AccountId: account, SandboxId: sandbox}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("no volumes = %v", err)
	}
}
