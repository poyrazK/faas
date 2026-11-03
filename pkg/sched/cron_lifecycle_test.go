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
