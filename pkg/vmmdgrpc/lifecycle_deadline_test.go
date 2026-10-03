// adr: 137

package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestStartupDeadlineForwardedToWakeRequest(t *testing.T) {
	request, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", StartupDeadlineS: 42, ExecutionMode: "worker", DisableStartupCpuBoost: true},
	})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if request.StartupDeadlineS != 42 {
		t.Fatalf("startup deadline = %d, want 42", request.StartupDeadlineS)
	}
	if request.ExecutionMode != "worker" {
		t.Fatalf("execution mode = %q, want worker", request.ExecutionMode)
	}
	if !request.DisableStartupCPUBoost {
		t.Fatal("disable_startup_cpu_boost was not forwarded to WakeRequest")
	}
}

func TestGRPCHealthcheckForwardedToWakeRequest(t *testing.T) {
	request, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App: &vmmdpb.AppSpec{
			BaseKey: "/b", HealthcheckGrpc: true,
			HealthcheckGrpcService: "catalog.v1.Catalog",
		},
	})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if !request.HealthcheckGRPC || request.HealthcheckGRPCService != "catalog.v1.Catalog" {
		t.Fatalf("gRPC healthcheck = (%t, %q), want (true, catalog.v1.Catalog)", request.HealthcheckGRPC, request.HealthcheckGRPCService)
	}
}

func TestReadinessProbeModesAreMutuallyExclusiveAtWireBoundary(t *testing.T) {
	tests := []struct {
		name string
		app  *vmmdpb.AppSpec
	}{
		{name: "http and grpc", app: &vmmdpb.AppSpec{HealthcheckPath: "/healthz", HealthcheckGrpc: true}},
		{name: "service without grpc mode", app: &vmmdpb.AppSpec{HealthcheckGrpcService: "catalog.v1.Catalog"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
				Instance: "inst-1",
				App:      tt.app,
			})
			if err == nil {
				t.Fatal("toWakeRequest accepted conflicting readiness probe fields")
			}
		})
	}
}

func TestStartupDeadlineForwardedToColdBootRequest(t *testing.T) {
	request, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", StartupDeadlineS: 42, ExecutionMode: "job", DisableStartupCpuBoost: true},
	})
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if request.StartupDeadlineS != 42 {
		t.Fatalf("startup deadline = %d, want 42", request.StartupDeadlineS)
	}
	if request.ExecutionMode != "job" {
		t.Fatalf("execution mode = %q, want job", request.ExecutionMode)
	}
	if !request.DisableStartupCPUBoost {
		t.Fatal("disable_startup_cpu_boost was not forwarded to cold-boot request")
	}
}

func TestGRPCHealthcheckForwardedToColdBootRequest(t *testing.T) {
	request, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "inst-1",
		App: &vmmdpb.AppSpec{
			BaseKey: "/b", HealthcheckGrpc: true,
			HealthcheckGrpcService: "catalog.v1.Catalog",
		},
	})
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if !request.HealthcheckGRPC || request.HealthcheckGRPCService != "catalog.v1.Catalog" {
		t.Fatalf("gRPC healthcheck = (%t, %q), want (true, catalog.v1.Catalog)", request.HealthcheckGRPC, request.HealthcheckGRPCService)
	}
}
