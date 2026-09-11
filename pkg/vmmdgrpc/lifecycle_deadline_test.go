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
		App:      &vmmdpb.AppSpec{BaseKey: "/b", StartupDeadlineS: 42, ExecutionMode: "worker"},
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
}

func TestStartupDeadlineForwardedToColdBootRequest(t *testing.T) {
	request, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/b", StartupDeadlineS: 42, ExecutionMode: "job"},
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
}
