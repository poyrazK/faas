// adr: 20261102160622 — the reaper tick's bulk read only rules instances out
// of the strict compute sweep: a stop re-reads the lease and the row under the
// app lock.
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStrictComputeSweepRereadsAPreloadedLeaseBeforeStopping(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "preload@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "preload", RAMMB: 256, MaxConcurrency: 4, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:preload", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	woke, err := engine.Wake(ctx, app.ID, dep.ID, "", sched.TriggerGateway)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.InstanceByID(ctx, woke.InstanceID)
	if err != nil || state.State(ins.State) != state.StateRunning {
		t.Fatalf("woken instance = %+v, %v", ins, err)
	}

	// The tick read a strict lease that had run out; the store holds no such
	// lease now (a renewal or a new attempt replaced it). The instance keeps
	// running.
	ranOut := financial.ComputeLease{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, InstanceID: ins.ID,
		Strict: true, StartedAt: time.Now().Add(-time.Hour), RunUntil: time.Now().Add(-time.Minute)}
	if err := engine.ReconcileFinancialBudgetStopsWithPreload(ctx, app.ID, []state.Instance{ins},
		map[string]financial.ComputeLease{ins.ID: ranOut}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.InstanceByID(ctx, ins.ID); err != nil || state.State(got.State) != state.StateRunning {
		t.Fatalf("instance after a stale preloaded lease = %s, %v; want running", got.State, err)
	}

	// The tick's preload agrees with the store: nothing to stop.
	if err := engine.ReconcileFinancialBudgetStopsForTick(ctx, []state.App{app}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.InstanceByID(ctx, ins.ID); err != nil || state.State(got.State) != state.StateRunning {
		t.Fatalf("instance after the tick sweep = %s, %v; want running", got.State, err)
	}
}
