package vmmdgrpc_test

// adr: 20261122132055 §7 — a stopping vmmd sends evacuate on every live
// session's stream and waits, bounded, until the owners have moved them.

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestEvacuateSandboxesAsksAndWaits(t *testing.T) {
	impl := vmmdgrpc.New(newSandboxRunVMM(), wire.NewOpsMetrics("vmmd_sandbox_evacuate_test"), "1.0.0", nil).WithAgentRuns(true)
	srv := grpc.NewServer()
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
	cli := vmmdpb.NewVmmdClient(conn)

	if left := impl.EvacuateSandboxes(context.Background(), time.Second, nil); left != 0 {
		t.Fatalf("no sessions: %d left", left)
	}
	stream := bootSandbox(t, cli)
	done := make(chan int, 1)
	go func() { done <- impl.EvacuateSandboxes(context.Background(), 10*time.Second, nil) }()
	ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetEvacuate() != nil })
	if ev.GetEvacuate().GetReason() != "vmmd_stopping" {
		t.Fatalf("evacuate = %v", ev.GetEvacuate())
	}
	// The owner moves the session (here: closes it); the wait ends.
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case left := <-done:
		if left != 0 {
			t.Fatalf("%d sessions left after the close", left)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the evacuation did not end with the stream")
	}

	// A session whose owner never moves it is left at the deadline.
	_ = bootSandbox(t, cli)
	if left := impl.EvacuateSandboxes(context.Background(), 300*time.Millisecond, nil); left != 1 {
		t.Fatalf("an unmoved session: %d left", left)
	}
}
