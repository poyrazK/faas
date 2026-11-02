// spec: §6.2 — a wake the cap refuses during a restart waits for the booting replacement; it admits nothing.
package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// PulseWatch F3: during `restart --fresh` at max_concurrency 1 the refresh's
// replacement held the app's only slot while it booted, and gateway wakes were
// refused at once with plan_limit_concurrency (63 429s on production-us) even
// though the replacement ran a second later. A wake refused while the app
// restarts now waits for the replacement booting in its deployment.
func TestWakeAtCapacityWaitsForBootingInstanceOfItsDeployment(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, 1)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	first, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("first Wake: %v", err)
	}
	// The slot's holder is a restart's booting replacement.
	defer e.MarkRestartInFlight(app.ID)()
	if err := store.UpdateInstanceState(ctx, first.InstanceID, string(state.StateColdBooting)); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = store.UpdateInstanceState(context.Background(), first.InstanceID, string(state.StateRunning))
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	got, err := e.Wake(waitCtx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake while the slot's instance boots = %v, want that instance once it runs", err)
	}
	if got.InstanceID != first.InstanceID {
		t.Fatalf("Wake returned %q, want the booting instance %q", got.InstanceID, first.InstanceID)
	}
	if n := e.Ledger().Concurrency(app.ID); n != 1 {
		t.Fatalf("concurrency = %d, want 1 (no second admission)", n)
	}
}

func TestWakeAtCapacityWithoutABootStillRefuses(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, 1)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	first, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("first Wake: %v", err)
	}
	// The app restarts, but the slot's holder is draining, not booting:
	// nothing is coming up.
	defer e.MarkRestartInFlight(app.ID)()
	if err := store.UpdateInstanceState(ctx, first.InstanceID, string(state.StateDraining)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = e.Wake(ctx, app.ID, "", "", "")
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodePlanLimitConcur {
		t.Fatalf("Wake with the slot draining = %v, want %s", err, api.CodePlanLimitConcur)
	}
	if waited := time.Since(started); waited > time.Second {
		t.Fatalf("refusal waited %s with nothing booting", waited)
	}
}
