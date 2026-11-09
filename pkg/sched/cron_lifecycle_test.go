// adr: 20261026201726
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// heldBudgetStore holds every app under a strict app compute budget.
type heldBudgetStore struct {
	MemStore *state.MemStore
	held     bool
}

func (f heldBudgetStore) FinancialComputePolicies(_ context.Context, _, appID string) ([]state.FinancialBudget, error) {
	return []state.FinancialBudget{{ID: "budget-1", Spec: financial.BudgetSpec{
		Name: "cap", Enabled: true, Mode: "strict", Scope: financial.BudgetScope{Kind: "app", ID: appID},
		Basis: "gross_usage", Action: "suspend_workloads", Meters: []string{"compute"},
	}}}, nil
}

func (f heldBudgetStore) FinancialComputeBalance(context.Context, state.FinancialBudget, time.Time) (financial.ComputeBalance, error) {
	return financial.ComputeBalance{Held: f.held}, nil
}

// A cron of an app its compute budget holds queues nothing: the drain would
// defer every run and deliver the backlog at once when the hold lifted. The
// cron stays due and runs once after the hold (crons audit 2026-11-02).
func TestCronSweepSkipsBudgetHeldApps(t *testing.T) {
	t.Parallel()
	mem := state.NewMemStore()
	acct, _ := mem.CreateAccount(t.Context(), "budget-held@example.com", api.PlanPro)
	c := hourlyCron(t, mem, acct.ID, "budget-held", true, state.CronOptions{})
	cronTestLoop(t, statetest.NewMem(heldBudgetStore{MemStore: mem, held: true})).RunCronTick(t.Context())
	if runs := queuedCronRuns(t, mem, acct.ID); len(runs) != 0 {
		t.Fatalf("queued runs while held = %d, want 0", len(runs))
	}
	if got, _ := mem.CronByID(t.Context(), c.ID); !got.LastFiredAt.IsZero() {
		t.Fatalf("held cron consumed its occurrence at %v", got.LastFiredAt)
	}
	cronTestLoop(t, statetest.NewMem(heldBudgetStore{MemStore: mem})).RunCronTick(t.Context())
	if runs := queuedCronRuns(t, mem, acct.ID); len(runs) != 1 {
		t.Fatalf("queued runs after the hold = %d, want 1", len(runs))
	}
}

// A queued run whose cron is gone is cancelled, not delivered: a cron removed
// by a deploy's gregale.yaml, a project apply or an app's deletion left its
// runs to reach the app (crons audit 2026-11-02).
func TestDrainCancelsRunsOfDeletedCrons(t *testing.T) {
	ctx := context.Background()
	d, store, _, _, synth := sched.NewDrainHarness(t, api.PlanHobby, true)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	// DeleteCron cancels its own runs; these runs' crons went another way, so
	// the drain is what stops them: one names a cron that no longer exists,
	// the other lost its cron_id to the foreign key's SET NULL.
	gone := "00000000-0000-4000-8000-0000000000aa"
	now := time.Now().UTC()
	queued := map[string]*string{"deleted cron": &gone, "detached cron": nil}
	ids := map[string]string{}
	for name, id := range queued {
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: apps[0].ID, AccountID: apps[0].AccountID, CronID: id,
			Source: state.InvocationCron, Method: "POST", Path: "/tick", DueAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = inv.ID
	}
	before := (*synth.Calls()).Load()
	d.Tick(ctx)
	if calls := (*synth.Calls()).Load() - before; calls != 0 {
		t.Fatalf("deliveries of a deleted cron's runs = %d, want 0", calls)
	}
	for name, id := range ids {
		inv, err := store.InvocationByID(ctx, id)
		if err != nil || inv.State != state.InvocationCancelled {
			t.Fatalf("%s run = %+v (err %v), want cancelled", name, inv, err)
		}
	}
}

// A queued run of a cron that was disabled or suspended after it was queued is
// cancelled, and one of a cron whose path changed goes to the new path. Both
// used to reach the app as queued: a disabled cron's runs the spend cap held
// back were delivered when the cap was raised (crons audit 2026-11-05).
func TestDrainFollowsTheCronsCurrentState(t *testing.T) {
	ctx := context.Background()
	d, store, _, _, synth := sched.NewDrainHarness(t, api.PlanHobby, true)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	app := apps[0]
	enqueue := func(cronID string) string {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: app.ID, AccountID: app.AccountID, CronID: &cronID,
			Source: state.InvocationCron, Method: "POST", Path: "/old", DueAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return inv.ID
	}
	wantState := func(id string, want state.InvocationState) {
		t.Helper()
		if inv, err := store.InvocationByID(ctx, id); err != nil || inv.State != want {
			t.Fatalf("run = %+v (err %v), want %s", inv, err, want)
		}
	}
	disabled, err := store.CreateCron(ctx, app.ID, "* * * * *", "/old", true)
	if err != nil {
		t.Fatal(err)
	}
	disabledRun := enqueue(disabled.ID)
	off := false
	if _, err := store.UpdateCron(ctx, disabled.ID, nil, nil, &off, nil); err != nil {
		t.Fatal(err)
	}
	d.Tick(ctx)
	wantState(disabledRun, state.InvocationCancelled)

	moved, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/old", true)
	if err != nil {
		t.Fatal(err)
	}
	movedRun := enqueue(moved.ID)
	newPath := "/new"
	if _, err := store.UpdateCron(ctx, moved.ID, nil, &newPath, nil, nil); err != nil {
		t.Fatal(err)
	}
	before := (*synth.Calls()).Load()
	d.Tick(ctx)
	if (*synth.Calls()).Load() == before || synth.LastPath() != "/new" {
		t.Fatalf("moved cron's run delivered to %q (calls %d), want /new", synth.LastPath(), (*synth.Calls()).Load()-before)
	}
	wantState(movedRun, state.InvocationCompleted)

	suspendedRun := enqueue(moved.ID)
	suspender, ok := store.(state.CronSuspensionStore)
	if !ok {
		t.Fatalf("%T cannot suspend crons", store)
	}
	if _, err := suspender.SuspendCronsForApp(ctx, app.ID, state.CronSuspendedNoLiveDeployment); err != nil {
		t.Fatal(err)
	}
	d.Tick(ctx)
	wantState(suspendedRun, state.InvocationCancelled)
}

// A manual run claimed but never linked to a queued run, as when schedd
// stopped between the two writes, is failed with a reason instead of staying
// running forever; a fresh one is left alone (crons audit 2026-11-02).
func TestInterruptedFireNowRequestsAreFailed(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(t.Context(), "interrupted@example.com", api.PlanPro)
	_, c := sched.NewAppAndCron(t, store, acct.ID, true)
	old, err := store.InsertFireNowRequest(t.Context(), c.ID, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimPendingFireNowRequest(t.Context()); err != nil {
		t.Fatal(err)
	}
	loop := cronTestLoop(t, store).WithClock(func() time.Time { return time.Now().Add(5 * time.Minute) })
	loop.FailInterruptedFireNowRequests(t.Context())
	got, err := store.GetFireNowRequest(t.Context(), old)
	if err != nil || got.Status != state.FireNowStatusFailed || got.Error == nil {
		t.Fatalf("interrupted request = %+v (err %v), want failed with a reason", got, err)
	}

	fresh, err := store.InsertFireNowRequest(t.Context(), c.ID, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimPendingFireNowRequest(t.Context()); err != nil {
		t.Fatal(err)
	}
	cronTestLoop(t, store).FailInterruptedFireNowRequests(t.Context())
	if got, err := store.GetFireNowRequest(t.Context(), fresh); err != nil || got.Status != state.FireNowStatusRunning {
		t.Fatalf("fresh request = %+v (err %v), want still running", got, err)
	}
}
