package sched

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkerScaleInStabilizer(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	key := workerPoolKey{appID: "app", scope: "default", deploymentID: "dep"}
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

	cases := []struct {
		name  string
		steps []struct{ at, current, desired, want int }
	}{
		{"scale-out is never delayed", []struct{ at, current, desired, want int }{
			{0, 1, 4, 4},
		}},
		{"first window holds the running size", []struct{ at, current, desired, want int }{
			{0, 3, 0, 3}, {30, 3, 0, 3}, {59, 3, 1, 3},
		}},
		{"shrinks to the window peak once observed for a window", []struct{ at, current, desired, want int }{
			{0, 4, 4, 4}, {30, 4, 2, 4}, {61, 4, 0, 2}, {91, 2, 0, 0},
		}},
		{"peak never exceeds the running size", []struct{ at, current, desired, want int }{
			{0, 2, 5, 5}, {30, 2, 5, 5}, {61, 2, 0, 2}, {91, 2, 0, 0},
		}},
		{"a refill inside the window keeps the pool", []struct{ at, current, desired, want int }{
			{0, 3, 3, 3}, {61, 3, 3, 3}, {62, 3, 0, 3}, {63, 3, 3, 3}, {64, 3, 0, 3},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWorkerScaleInStabilizer(time.Minute)
			for i, step := range tc.steps {
				if got := s.stabilize(key, step.current, step.desired, at(step.at)); got != step.want {
					t.Fatalf("step %d (t=%ds current=%d desired=%d) = %d, want %d", i, step.at, step.current, step.desired, got, step.want)
				}
			}
		})
	}
}

func TestWorkerScaleInStabilizerDisabledAndKeyed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, s := range []*workerScaleInStabilizer{nil, newWorkerScaleInStabilizer(0)} {
		if got := s.stabilize(workerPoolKey{appID: "a"}, 3, 0, now); got != 0 {
			t.Fatalf("disabled stabilizer held the pool at %d", got)
		}
	}
	s := newWorkerScaleInStabilizer(time.Minute)
	old := workerPoolKey{appID: "a", scope: "default", deploymentID: "old"}
	s.stabilize(old, 3, 3, now)
	s.stabilize(old, 3, 3, now.Add(61*time.Second))
	// A new generation starts its own window instead of inheriting the peak.
	fresh := workerPoolKey{appID: "a", scope: "default", deploymentID: "new"}
	if got := s.stabilize(fresh, 2, 0, now.Add(62*time.Second)); got != 2 {
		t.Fatalf("new generation shrank before its first window: %d", got)
	}
	// Pools that stop reporting are forgotten after two windows.
	s.stabilize(fresh, 2, 2, now.Add(200*time.Second))
	if _, ok := s.pools[old]; ok {
		t.Fatal("stale pool history was not swept")
	}
}

func TestNewEngineAppliesDefaultWorkerScaleInWindow(t *testing.T) {
	e, err := NewEngine(context.Background(), state.NewMemStore(), NewNodeLedger(), &fakeVMM{}, &fakeNotifier{}, "1.10.0", testLog())
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Duration(api.WorkerScaleInStabilizationSeconds) * time.Second; e.workerScaleIn == nil || e.workerScaleIn.window != want {
		t.Fatalf("default worker scale-in window = %+v, want %s", e.workerScaleIn, want)
	}
}

func TestWorkerPoolScaleInWaitsForStabilizationWindow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	seedScopedWorker(t, store, app, deps["default"])
	seedScopedWorker(t, store, app, deps["default"])
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithWorkerScaleInStabilization(time.Minute)
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	for _, offset := range []time.Duration{0, 30 * time.Second, 59 * time.Second} {
		engine.now = func() time.Time { return t0.Add(offset) }
		if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
			t.Fatal(err)
		}
		if got := scopedWorkerCounts(t, store, app.ID)["default"]; got != 2 || vmm.stopInstanceOnNodeN != 0 {
			t.Fatalf("t+%s: empty queue scaled in early: workers=%d stops=%d", offset, got, vmm.stopInstanceOnNodeN)
		}
	}
	engine.now = func() time.Time { return t0.Add(61 * time.Second) }
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	if got := scopedWorkerCounts(t, store, app.ID)["default"]; got != 0 || vmm.stopInstanceOnNodeN != 2 {
		t.Fatalf("stable empty queue did not scale in: workers=%d stops=%d", got, vmm.stopInstanceOnNodeN)
	}
}

func TestWorkerPoolExplicitReplicaCountBypassesStabilization(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	seedScopedWorker(t, store, app, deps["default"])
	seedScopedWorker(t, store, app, deps["default"])
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithWorkerScaleInStabilization(time.Hour)
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPoolForScope(ctx, app.ID, "default", 1, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	if got := scopedWorkerCounts(t, store, app.ID)["default"]; got != 1 || vmm.stopInstanceOnNodeN != 1 {
		t.Fatalf("explicit replica count was stabilized: workers=%d stops=%d", got, vmm.stopInstanceOnNodeN)
	}
}

func TestWorkerPoolScaleInStopsIdleWorkerBeforeBusyOne(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deps := seedWorkerScopes(t, store, api.PlanPro)
	workers := []state.Instance{
		seedScopedWorker(t, store, app, deps["default"]),
		seedScopedWorker(t, store, app, deps["default"]),
	}
	// Without telemetry the pool keeps its oldest worker. Make that one idle
	// and the worker age-ordering would stop busy, so only the busy rank can
	// produce the expected victim.
	sort.SliceStable(workers, func(i, j int) bool {
		if workers[i].StartedAt.Equal(workers[j].StartedAt) {
			return workers[i].ID < workers[j].ID
		}
		return workers[i].StartedAt.Before(workers[j].StartedAt)
	})
	idle, busy := workers[0], workers[1]
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	engine.now = func() time.Time { return now }
	engine.telemetryCache.Replace(idle.NodeID, now, now, []NodeTelemetry{
		{InstanceID: idle.ID, InflightRequests: 0},
		{InstanceID: busy.ID, InflightRequests: 3},
	})
	if err := engine.ReconcileWorkerPoolForScope(ctx, app.ID, "default", 1, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	afterIdle, _ := store.InstanceByID(ctx, idle.ID)
	afterBusy, _ := store.InstanceByID(ctx, busy.ID)
	if afterIdle.State != string(state.StateStopped) || afterBusy.State != string(state.StateRunning) {
		t.Fatalf("scale-in victim: idle=%s busy=%s", afterIdle.State, afterBusy.State)
	}
}

func TestWorkerBusyRankTreatsUnknownTelemetryAsPossiblyBusy(t *testing.T) {
	engine := newEngine(t, state.NewMemStore(), &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	now := time.Now()
	engine.telemetryCache.Replace("node", now, now, []NodeTelemetry{
		{InstanceID: "busy", InflightRequests: 1},
		{InstanceID: "idle"},
	})
	busy := engine.workerBusyRank(state.Instance{ID: "busy"}, now)
	unknown := engine.workerBusyRank(state.Instance{ID: "unreported"}, now)
	idle := engine.workerBusyRank(state.Instance{ID: "idle"}, now)
	stale := engine.workerBusyRank(state.Instance{ID: "idle"}, now.Add(TelemetryFreshness+time.Second))
	if !(busy < unknown && unknown < idle) || stale != unknown {
		t.Fatalf("ranks busy=%d unknown=%d idle=%d stale=%d", busy, unknown, idle, stale)
	}
}
