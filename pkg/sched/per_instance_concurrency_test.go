// spec: §6.2
// adr: 20261022203523

package sched_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func setPerInstanceConcurrency(t *testing.T, store state.Store, appID string, n int) {
	t.Helper()
	if _, err := store.UpdateApp(context.Background(), appID, state.UpdateAppParams{
		ScalingPolicy:    &state.ScalingPolicy{PerInstanceConcurrency: n},
		SetScalingPolicy: true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
}

// TestWakeFanoutDividesByDeclaredPerInstanceConcurrency pins that the wake
// coordinator's per-instance denominator is the app's declaration, not the
// plan's concurrency_per_vm (Hunt 12 H12-4).
func TestWakeFanoutDividesByDeclaredPerInstanceConcurrency(t *testing.T) {
	pro := api.MustLimitsFor(api.PlanPro).ConcurrencyPerVMBound
	for _, tc := range []struct {
		name     string
		declared int
		want     int
	}{
		{name: "unset", declared: 0, want: pro},
		{name: "declared", declared: 2, want: 2},
		{name: "above the plan bound", declared: pro + 1, want: pro},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, 3)
			setPerInstanceConcurrency(t, store, app.ID, tc.declared)
			engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			fanout := engine.WakeFanoutFor(context.Background(), app.ID)
			if fanout.PerVM != tc.want || fanout.MaxInFlight != 3 {
				t.Fatalf("fanout = %+v, want PerVM %d MaxInFlight 3", fanout, tc.want)
			}
		})
	}
}

// TestPropertyWakeFanoutNeverWantsPastTheCeiling is invariant §6.2-1 on the
// fan-out side: whatever per-instance concurrency an app declares, the
// coordinator never asks for a wake once running, starting and in-flight
// wakes reach the app's instance ceiling. A smaller denominator only makes
// it ask sooner.
func TestPropertyWakeFanoutNeverWantsPastTheCeiling(t *testing.T) {
	bound := api.MustLimitsFor(api.PlanScale).ConcurrencyPerVMBound
	for perVM := 1; perVM <= bound; perVM++ {
		for ceiling := 1; ceiling <= api.MustLimitsFor(api.PlanScale).MaxConcurrency; ceiling++ {
			for existing := 0; existing <= ceiling; existing++ {
				for inFlight := 0; existing+inFlight <= ceiling; inFlight++ {
					for _, waiting := range []int{1, 2, 3, perVM, perVM + 1, 2*perVM + 1, 512} {
						fanout := sched.WakeFanout{MaxInFlight: ceiling, PerVM: perVM, Existing: existing}
						wants := fanout.Wants(inFlight, waiting)
						if wants && existing+inFlight >= ceiling {
							t.Fatalf("perVM=%d ceiling=%d existing=%d inFlight=%d waiting=%d wants a wake at the ceiling",
								perVM, ceiling, existing, inFlight, waiting)
						}
						if want := existing+inFlight < ceiling && waiting > (existing+inFlight)*perVM; wants != want {
							t.Fatalf("perVM=%d ceiling=%d existing=%d inFlight=%d waiting=%d: wants=%v, want %v",
								perVM, ceiling, existing, inFlight, waiting, wants, want)
						}
					}
				}
			}
		}
	}
}

// TestPropertyEnsureWakeBurstFansOutToTheCeilingAndNoFurther drives a cold
// burst through the real coordinator and ledger. With one request per
// instance, twenty callers must start exactly max_concurrency boots (the plan
// bound of 25 would have coalesced them onto one), and the ledger must never
// hold more than the ceiling, whichever order the callers arrive in.
func TestPropertyEnsureWakeBurstFansOutToTheCeilingAndNoFurther(t *testing.T) {
	const ceiling, callers = 3, 20
	for round := range 5 {
		store := state.NewMemStore()
		_, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, ceiling)
		setPerInstanceConcurrency(t, store, app.ID, 1)
		vmm := &sched.FakeVMM{}
		release := make(chan struct{})
		booting := make(chan struct{}, callers)
		*vmm.ColdBootHook() = func() {
			booting <- struct{}{}
			<-release
		}
		engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")

		var wg sync.WaitGroup
		errs := make(chan error, callers)
		for range callers {
			wg.Go(func() {
				if _, err := engine.EnsureWake(context.Background(), app.ID, ""); err != nil {
					errs <- err
				}
			})
		}
		for i := range ceiling {
			select {
			case <-booting:
			case <-time.After(5 * time.Second):
				t.Fatalf("round %d: %d of %d boots started for %d waiting callers at one request per instance", round, i, ceiling, callers)
			}
		}
		if got := engine.Ledger().Concurrency(app.ID); got > ceiling {
			t.Fatalf("round %d: ledger concurrency %d exceeds max_concurrency %d", round, got, ceiling)
		}
		close(release)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("round %d: EnsureWake: %v", round, err)
		}
		vmm.Mu().Lock()
		boots := *vmm.ColdBoots()
		vmm.Mu().Unlock()
		if boots != ceiling {
			t.Errorf("round %d: cold boots = %d, want exactly max_concurrency %d", round, boots, ceiling)
		}
		if got := engine.Ledger().Concurrency(app.ID); got > ceiling {
			t.Errorf("round %d: ledger concurrency %d exceeds max_concurrency %d", round, got, ceiling)
		}
	}
}

// TestLoopReaperAggressiveHoldsDeclaredPerInstanceDemand pins the scale-in
// floor: 30 requests in flight at a declared 10 per instance need 3
// instances, so the reaper keeps 3 plus its +1 buffer. At Pro's plan bound of
// 25 it would keep only ceil(30/25)+1 = 3 and park an instance the requests
// still need.
func TestLoopReaperAggressiveHoldsDeclaredPerInstanceDemand(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	rps := 10
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    &rps,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	setPerInstanceConcurrency(t, store, app.ID, 10)
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	sched.WakeN(t, engine, app.ID, 5)

	frozen := time.Now().Add(35 * time.Second)
	loop := sched.NewLoop(nil, engine, sched.TestLog()).
		WithClock(func() time.Time { return frozen }).
		WithRecentLoad(sched.StalledSurgeMirror(app.ID, 30, frozen)).
		WithReaperAggressive(true)
	loop.RunReaper(context.Background())

	if running := sched.LiveCount(t, store, app.ID); running != 4 {
		t.Errorf("running = %d, want 4 (30 in flight at 10 per instance need 3, plus the +1 buffer)", running)
	}
}
