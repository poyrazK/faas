package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// countingWatchdogStore counts the watchdog's stuck-row reads.
type countingWatchdogStore struct {
	MemStore *state.MemStore
	reads    int
}

func (s *countingWatchdogStore) ListInstancesByStatesOlderThan(ctx context.Context, states []state.State, threshold time.Time) ([]state.Instance, error) {
	s.reads++
	return s.MemStore.ListInstancesByStatesOlderThan(ctx, states, threshold)
}

// TestWatchdogSweepReadsOnceAndKeepsEachBudget pins the sweep's single read:
// one query per tick for all three buckets while nothing is stuck, and each
// bucket still applying its own budget. A cold boot past the WAKING budget but
// inside its own stays; a stuck wake still falls back to a cold boot and is
// killed in the same tick.
func TestWatchdogSweepReadsOnceAndKeepsEachBudget(t *testing.T) {
	ctx := context.Background()
	f := newFailoverFleet(t, 1, 8192, func(string) error { return nil })
	node := f.nodes[0]
	app := f.app(t, api.PlanPro, 256, 3)
	dep, err := f.store.LiveDeployment(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	create := func(st state.State, age time.Duration) string {
		t.Helper()
		ins, err := f.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(st), 256, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
		if err != nil {
			t.Fatal(err)
		}
		f.store.BackdateForTest(ins.ID, time.Now().Add(-age))
		return ins.ID
	}
	youngBoot := create(state.StateColdBooting, sched.WakingSweepBudget+5*time.Second)
	counting := &countingWatchdogStore{MemStore: f.store}
	watchdog := sched.NewWatchdog(statetest.NewMem(counting), f.e, nil).WithClock(time.Now)
	watchdog.SweepRuns(ctx)
	if counting.reads != 1 {
		t.Fatalf("watchdog reads per idle sweep = %d, want 1", counting.reads)
	}

	staleWake := create(state.StateWaking, sched.WakingSweepBudget+sched.ColdBootSweepBudget+time.Minute)
	staleBoot := create(state.StateColdBooting, sched.ColdBootSweepBudget+time.Minute)
	watchdog.SweepRuns(ctx)
	for id, want := range map[string]bool{staleWake: true, staleBoot: true, youngBoot: false} {
		ins, err := f.store.InstanceByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		killed := ins.State == string(state.StateFailed)
		if killed != want {
			t.Errorf("instance %s state %s, killed=%v want %v", id, ins.State, killed, want)
		}
	}
}
