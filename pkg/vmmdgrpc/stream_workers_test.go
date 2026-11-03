package vmmdgrpc_test

import (
	"context"
	"net"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// Long-lived streams (raw, SSE) can hold every stream worker. A new call must
// still be served at once rather than queue behind them.
func TestStreamWorkersNeverQueueACallBehindBusyWorkers(t *testing.T) {
	srv := grpc.NewServer(vmmdgrpc.StreamWorkers())
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthServer)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := healthpb.NewHealthClient(conn)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range runtime.GOMAXPROCS(0) + 2 {
		watch, err := client.Watch(ctx, &healthpb.HealthCheckRequest{})
		if err != nil {
			t.Fatal(err)
		}
		// The first status arrives once the handler runs; the stream then
		// stays open, holding its worker.
		if _, err := watch.Recv(); err != nil {
			t.Fatal(err)
		}
	}
	checkCtx, checkCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer checkCancel()
	resp, err := client.Check(checkCtx, &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("call behind busy workers: %v %v", resp.GetStatus(), err)
	}
}
