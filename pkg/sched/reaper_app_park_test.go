package sched_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// adr: 20261103164725 — production-us rc.319 (2026-11-04): the reaper
// submitted a whole-app park for every parked app on every tick. The pool
// runs four at once and drops the rest, so nine parked apps with nothing live
// filled the slots in the same order and the same five were dropped every
// 10 s. Only an app with something live takes a slot.
func TestReaperParksOnlyParkedAppsWithSomethingLive(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	parked := state.AppEvictedCold
	account, live, _ := sched.SeedApp(t, store, api.PlanPro, 256, 4)
	for i := range 6 {
		app, err := store.CreateApp(ctx, state.App{
			AccountID: account.ID, Slug: fmt.Sprintf("parked-%d", i), RAMMB: 256, MaxConcurrency: 4, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
			t.Fatal(err)
		}
	}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	woken, err := engine.Wake(ctx, live.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := store.UpdateApp(ctx, live.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}

	ops := wire.NewOpsMetrics("schedd")
	sched.NewLoop(nil, engine, sched.TestLog()).WithOpsMetrics(ops).XRunReaper(ctx)

	queued := testutil.ToFloat64(ops.LoopWork("app_park", "queued"))
	dropped := testutil.ToFloat64(ops.LoopWork("app_park", "dropped"))
	if queued != 1 || dropped != 0 {
		t.Fatalf("app_park queued=%v dropped=%v; want one park, for the app with a live instance", queued, dropped)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ins, err := store.InstanceByID(ctx, woken.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		if !state.IsLive(ins.State) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance of the parked app is still %s", ins.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
