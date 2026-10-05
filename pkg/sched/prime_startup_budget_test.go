// adr: 138

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrimeStartupExtension(t *testing.T) {
	for _, tc := range []struct {
		deadlineS int32
		want      time.Duration
	}{
		{0, 0}, {15, 0}, {30, 0},
		{31, 16 * time.Second},
		{60, 45 * time.Second},
		{120, 105 * time.Second},
		{300, 285 * time.Second},
	} {
		if got := primeStartupExtension(tc.deadlineS); got != tc.want {
			t.Errorf("primeStartupExtension(%d) = %s, want %s", tc.deadlineS, got, tc.want)
		}
	}
	e := &Engine{}
	if got := e.primeColdBootBudget(120); got != ColdBootTimeout+105*time.Second {
		t.Errorf("primeColdBootBudget(120) = %s, want %s", got, ColdBootTimeout+105*time.Second)
	}
	e.bootBudget = func(state.State) time.Duration { return time.Second }
	if got := e.primeColdBootBudget(120); got != time.Second {
		t.Errorf("test override ignored: primeColdBootBudget = %s", got)
	}
}

// TestColdBootWatchdogHonoursPrimeStartupDeadline reproduces production-us:
// a Scale app (ADR-138 default startup deadline 120 s) that listened after
// 40 s was killed 30.6 s into its first boot, and its deployment failed with
// "cold_boot_timeout". The watchdog now leaves a priming instance alone until
// its extended budget runs out. A Hobby prime (30 s deadline) and a Scale
// wake of a live deployment keep the spec budget.
func TestColdBootWatchdogHonoursPrimeStartupDeadline(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		plan    api.Plan
		status  state.DeploymentStatus
		age     time.Duration
		survive bool
	}{
		{"scale prime at 40s", api.PlanScale, state.DeploySnapshotting, 40 * time.Second, true},
		{"scale prime past extended budget", api.PlanScale, state.DeploySnapshotting, ColdBootSweepBudget + 106*time.Second, false},
		{"hobby prime at 31s", api.PlanHobby, state.DeploySnapshotting, 31 * time.Second, false},
		{"scale wake of live deployment at 31s", api.PlanScale, state.DeployLive, 31 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, tc.plan, 256, 5)
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, tc.status, ""); err != nil {
				t.Fatal(err)
			}
			ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateColdBooting), 256, state.DefaultLocalNodeName, "")
			if err != nil {
				t.Fatal(err)
			}
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			now := time.Now()
			NewWatchdog(store, engine, nil).WithClock(func() time.Time { return now.Add(tc.age) }).sweepRuns(ctx)
			survived := rowState(t, store, ins.ID) == string(state.StateColdBooting)
			if survived != tc.survive {
				t.Fatalf("instance survived=%v at %s, want %v", survived, tc.age, tc.survive)
			}
		})
	}
}
