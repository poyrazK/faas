// adr: 20261022184531
package vmmdgrpc_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Ping reports vmmd's base generation verdicts without waiting for shared
// storage: the first Ping starts the check, a later one carries its result.
func TestPing_ReportsBaseGenerationReadiness(t *testing.T) {
	refused := vmmdgrpc.BaseGenerationReadiness{BaseKey: "base/base-amd64.ext4", Generation: "ghcr.io/p/base-minimal@sha256:db529ed",
		Reason: "shared storage publishes ghcr.io/p/base-minimal@sha256:5ca69ea"}
	release := make(chan struct{})
	var checks atomic.Int32
	readiness := vmmdgrpc.NewCachedBaseGenerationReadiness(func(context.Context) []vmmdgrpc.BaseGenerationReadiness {
		checks.Add(1)
		<-release
		return []vmmdgrpc.BaseGenerationReadiness{refused}
	})
	srv := vmmdgrpc.New(&fakeVMM{}, wire.NewOpsMetrics("vmmd_test"), "1.17.0", nil)
	if resp, err := srv.Ping(context.Background(), &vmmdpb.PingRequest{}); err != nil || len(resp.GetBaseGenerations()) != 0 {
		t.Fatalf("Ping without a reporter = %v, %v", resp.GetBaseGenerations(), err)
	}
	srv.WithBaseGenerationReadiness(readiness)
	// The check blocks until released; Ping must answer anyway.
	if resp, err := srv.Ping(context.Background(), &vmmdpb.PingRequest{}); err != nil || len(resp.GetBaseGenerations()) != 0 {
		t.Fatalf("first Ping = %v, %v; want no report while the check runs", resp.GetBaseGenerations(), err)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := srv.Ping(context.Background(), &vmmdpb.PingRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.GetBaseGenerations(); len(got) == 1 {
			if got[0].GetBaseKey() != refused.BaseKey || got[0].GetGeneration() != refused.Generation || got[0].GetAdoptable() || got[0].GetReason() != refused.Reason {
				t.Fatalf("reported %v, want %+v", got[0], refused)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Ping never carried the check's report")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := checks.Load(); n != 1 {
		t.Fatalf("checks = %d, want 1 within the interval", n)
	}
}
