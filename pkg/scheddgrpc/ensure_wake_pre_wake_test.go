package scheddgrpc_test

// The EnsureWake RPC starts the pre-wake clock before authorization, so
// wake.boot_started's pre_wake_* spans cover the whole RPC up to Engine.Wake.

import (
	"context"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestEnsureWakeStartsThePreWakeClock(t *testing.T) {
	var started time.Time
	before := time.Now()
	cli := newServer(t, &ensureWakeEngine{
		fakeEngine: &fakeEngine{},
		ensureWakeFn: func(ctx context.Context, _, _ string) (sched.CoordOutcome, error) {
			started = sched.EnsureWakeRPCStartedAt(ctx)
			return sched.CoordOutcome{Instance: &sched.CoordInstance{InstanceID: "ins-1", NodeID: "node-A", WakeID: "wake-1"}}, nil
		},
	})
	if _, err := cli.EnsureWake(context.Background(), &scheddpb.EnsureWakeRequest{AppId: "app-1"}); err != nil {
		t.Fatalf("EnsureWake: %v", err)
	}
	if started.IsZero() || started.Before(before) {
		t.Fatalf("pre-wake clock start = %v, want a time after %v", started, before)
	}
}
