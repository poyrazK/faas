package vmmdgrpc_test

// adr: 20261122132055 §7 — a detachable session's VM outlives a lost
// stream: a reattach on the same instance and session takes it over, and
// one that never comes leaves it destroyed after the grace. A session that
// is not detachable, or a reattach of another session, finds nothing.

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
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

func newReattachClient(t *testing.T, vmm *sandboxRunVMM, grace time.Duration) vmmdpb.VmmdClient {
	t.Helper()
	cli, _ := newReattachServer(t, vmm, grace)
	return cli
}

func newReattachServer(t *testing.T, vmm *sandboxRunVMM, grace time.Duration) (vmmdpb.VmmdClient, *vmmdgrpc.Server) {
	t.Helper()
	srv := grpc.NewServer()
	impl := vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_sandbox_reattach_test"), "1.0.0", nil).WithAgentRuns(true).
		WithSandboxReattachGrace(grace)
	impl.Register(srv)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn), impl
}

// openSandbox sends start on a stream it can cancel and returns its first
// event.
func openSandbox(t *testing.T, cli vmmdpb.VmmdClient, start *vmmdpb.RunSandboxStart) (vmmdpb.Vmmd_RunSandboxClient, context.CancelFunc, *vmmdpb.RunSandboxEvent, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := cli.RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Start{Start: start}}); err != nil {
		t.Fatal(err)
	}
	ev, err := stream.Recv()
	return stream, cancel, ev, err
}

func detachableStart() *vmmdpb.RunSandboxStart {
	start := sandboxRunStart().GetStart()
	start.Detachable = true
	return start
}

func reattachStart(sandboxID string) *vmmdpb.RunSandboxStart {
	return &vmmdpb.RunSandboxStart{Instance: "sbx-vm", SandboxId: sandboxID, AccountId: "acct-1", Plan: "pro", Reattach: true}
}

func TestSandboxReattachTakesOverTheRunningVM(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli := newReattachClient(t, vmm, time.Minute)
	_, cancel, ev, err := openSandbox(t, cli, detachableStart())
	if err != nil || ev.GetBooted() == nil {
		t.Fatalf("boot = %v %v", ev, err)
	}
	// agentd's replica goes away: the stream is cut, the VM runs on.
	cancel()
	time.Sleep(100 * time.Millisecond)
	if woken, destroyed, _, _ := vmm.state(); woken != 1 || len(destroyed) != 0 {
		t.Fatalf("after the lost stream: %d woken, destroyed %v", woken, destroyed)
	}
	// Another session cannot take it.
	if _, _, _, err := openSandbox(t, cli, reattachStart("sbx-2")); status.Code(err) != codes.NotFound {
		t.Fatalf("a reattach of another session = %v, want NotFound", err)
	}
	stream, _, ev, err := openSandbox(t, cli, reattachStart("sbx-1"))
	if err != nil || !ev.GetBooted().GetReattached() {
		t.Fatalf("reattach = %v %v", ev, err)
	}
	if woken, destroyed, _, _ := vmm.state(); woken != 1 || len(destroyed) != 0 {
		t.Fatalf("after the reattach: %d woken, destroyed %v", woken, destroyed)
	}
	// The reattached stream serves the VM, and its close destroys it.
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { _, destroyed, _, _ := vmm.state(); return len(destroyed) == 1 })
	if _, _, _, err := openSandbox(t, cli, reattachStart("sbx-1")); status.Code(err) != codes.NotFound {
		t.Fatalf("a reattach after the close = %v, want NotFound", err)
	}
}

func TestDetachedSandboxDestroyedAfterTheGrace(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli := newReattachClient(t, vmm, 200*time.Millisecond)
	_, cancel, _, err := openSandbox(t, cli, detachableStart())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitUntil(t, func() bool { _, destroyed, _, _ := vmm.state(); return len(destroyed) == 1 })
	if _, _, _, err := openSandbox(t, cli, reattachStart("sbx-1")); status.Code(err) != codes.NotFound {
		t.Fatalf("a reattach after the grace = %v, want NotFound", err)
	}
}

func TestSandboxNotDetachableEndsWithItsStream(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli := newReattachClient(t, vmm, time.Minute)
	_, cancel, _, err := openSandbox(t, cli, sandboxRunStart().GetStart())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitUntil(t, func() bool { _, destroyed, _, _ := vmm.state(); return len(destroyed) == 1 })
}

// A new start of the instance replaces a detached VM no reattach came for:
// the old one is destroyed first, and its grace never touches the new one.
func TestSandboxRestartReplacesTheDetachedVM(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli := newReattachClient(t, vmm, 300*time.Millisecond)
	_, cancel, _, err := openSandbox(t, cli, detachableStart())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	stream, _, ev, err := openSandbox(t, cli, detachableStart())
	if err != nil || ev.GetBooted() == nil {
		t.Fatalf("second boot = %v %v", ev, err)
	}
	if woken, destroyed, _, _ := vmm.state(); woken != 2 || len(destroyed) != 1 {
		t.Fatalf("after the second boot: %d woken, destroyed %v", woken, destroyed)
	}
	// Past the first VM's grace the second still runs.
	time.Sleep(500 * time.Millisecond)
	if _, destroyed, _, _ := vmm.state(); len(destroyed) != 1 {
		t.Fatalf("the old grace destroyed the new VM: %v", destroyed)
	}
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { _, destroyed, _, _ := vmm.state(); return len(destroyed) == 2 })
}

// A stopping vmmd destroys the VMs it kept and keeps no more: sessions
// are not adopted, so nothing could reattach to them.
func TestStoppingVMMDKeepsNoDetachedSandbox(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli, srv := newReattachServer(t, vmm, time.Minute)
	_, cancel, _, err := openSandbox(t, cli, detachableStart())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	if n := srv.StopDetachingSandboxes(context.Background()); n != 1 {
		t.Fatalf("destroyed %d detached VMs, want 1", n)
	}
	if _, destroyed, _, _ := vmm.state(); len(destroyed) != 1 {
		t.Fatalf("destroyed %v", destroyed)
	}
	_, cancel, _, err = openSandbox(t, cli, detachableStart())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitUntil(t, func() bool { _, destroyed, _, _ := vmm.state(); return len(destroyed) == 2 })
}
