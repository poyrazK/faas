package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

func TestStopGRPCServerBoundsPersistentStream(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	t.Cleanup(server.Stop)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///shutdown-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := healthpb.NewHealthClient(conn).Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	drainCtx, cancelDrain := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancelDrain()
	done := make(chan struct{})
	go func() { stopGRPCServer(drainCtx, server); close(done) }()
	select {
	case <-done:
		if drainCtx.Err() == nil {
			t.Fatal("live stream was cut before drain deadline")
		}
	case <-ctx.Done():
		t.Fatal("persistent stream prevented bounded shutdown")
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("stream still open after forced stop")
	}
}

func TestStopGRPCServerDoesNotWaitWhenIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { stopGRPCServer(ctx, grpc.NewServer()); close(done) }()
	select {
	case <-done:
		if ctx.Err() != nil {
			t.Fatal("idle server waited for shutdown deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("idle server failed to stop promptly")
	}
}
