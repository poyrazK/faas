package fcvm

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestGRPCHealthcheckProbe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("catalog.v1.Catalog", healthpb.HealthCheckResponse_NOT_SERVING)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tests := []struct {
		name      string
		service   string
		wantReady bool
		wantErr   bool
	}{
		{name: "empty service checks overall status", wantReady: true},
		{name: "named service status", service: "catalog.v1.Catalog", wantReady: false},
		{name: "unknown service is an RPC failure", service: "missing.v1.Service", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			gotReady, gotErr := grpcHealthcheckProbe(ctx, conn, tt.service)
			if (gotErr != nil) != tt.wantErr {
				t.Fatalf("probe error = %v, wantErr %t", gotErr, tt.wantErr)
			}
			if gotReady != tt.wantReady {
				t.Fatalf("probe ready = %t, want %t", gotReady, tt.wantReady)
			}
		})
	}
}
