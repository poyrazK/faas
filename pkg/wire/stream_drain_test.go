package wire_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// TestStreamDrainLetsGracefulStopFinish: a peer's open watch stream no longer
// holds GracefulStop for its whole grace, and the peer is told to reconnect.
func TestStreamDrainLetsGracefulStopFinish(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	drain := wire.NewStreamDrain()
	srv := grpc.NewServer(drain.ServerOption())
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := healthpb.NewHealthClient(conn)
	watch, err := client.Watch(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Recv(); err != nil {
		t.Fatalf("first watch update: %v", err)
	}

	drain.Begin()
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("GracefulStop still waiting on the watch stream after the drain began")
	}
	_, err = watch.Recv()
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("watch ended with %v, want Unavailable so the peer reconnects", err)
	}
}

// TestStreamDrainLeavesStreamsAloneUntilBegin: the interceptor is inert while
// the daemon runs.
func TestStreamDrainLeavesStreamsAloneUntilBegin(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(wire.NewStreamDrain().ServerOption())
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	watch, err := healthpb.NewHealthClient(conn).Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Recv(); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("watch ended with %v before the drain began", err)
	}
}
