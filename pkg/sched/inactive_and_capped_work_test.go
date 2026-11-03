// adr: 1162 — amendment 2026-11-03 (2): a fork is refused at the spend cap,
// and an inactive account's running job runs are stopped.
package sched_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A fork is billed like any instance; at the spend cap it is refused before
// it boots instead of being parked by the next reaper tick.
func TestRestoreForkRefusedAtTheSpendCap(t *testing.T) {
	store := state.NewMemStore()
	fork := sched.SeedForkTarget(t, store, true)
	var reached atomic.Bool
	reached.Store(true)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOverageChecker(switchableCap(&reached))
	if _, err := e.RestoreFork(context.Background(), fork); !errors.Is(err, sched.ErrForkSpendCap) {
		t.Fatalf("RestoreFork at the cap = %v, want ErrForkSpendCap", err)
	}
	if code, _ := sched.ForkFailureCode(sched.ErrForkSpendCap); code != "spend_cap_reached" {
		t.Fatalf("failure code = %q, want spend_cap_reached", code)
	}
	reached.Store(false)
	if _, err := e.RestoreFork(context.Background(), fork); err != nil {
		t.Fatalf("RestoreFork under the cap: %v", err)
	}
}

func runningJobRun(t *testing.T, store *state.MemStore, accountID string) state.JobRun {
	t.Helper()
	ctx := context.Background()
	job, err := store.JobCreate(ctx, accountID, "job-"+uuid.NewString()[:8], "batch", "example/image", []string{"/job"}, 128, 600, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, accountID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	instance := uuid.NewString()
	if _, err := store.CreateAndClaimJobInstance(ctx, instance, job.ID, run.ID, 0, string(state.StateRunning), 128, "node-1", instance, uuid.NewString(), time.Now().Add(time.Hour), "node-1"); err != nil {
		t.Fatal(err)
	}
	if run, err = store.JobRunRecompute(ctx, run.ID); err != nil || run.AggregateStatus != "running" {
		t.Fatalf("seeded run = %+v (%v), want running", run, err)
	}
	return run
}

// A suspended account's running job runs are cancelled, as its apps are
// parked; an active account's runs are left alone.
func TestStopInactiveAccountJobRuns(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	active, _ := store.CreateAccount(ctx, "jobs-active@example.com", api.PlanPro)
	suspended, _ := store.CreateAccount(ctx, "jobs-suspended@example.com", api.PlanPro)
	keep := runningJobRun(t, store, active.ID)
	stop := runningJobRun(t, store, suspended.ID)
	if err := store.UpdateAccountStatus(ctx, suspended.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	stopped, err := e.StopInactiveAccountJobRuns(ctx)
	if err != nil || stopped != 1 {
		t.Fatalf("StopInactiveAccountJobRuns = %d, %v; want 1 run stopped", stopped, err)
	}
	if got, _ := store.JobRunGetByID(ctx, stop.ID); got.AggregateStatus != "cancelled" {
		t.Fatalf("suspended account's run = %s, want cancelled", got.AggregateStatus)
	}
	if got, _ := store.JobRunGetByID(ctx, keep.ID); got.AggregateStatus != "running" {
		t.Fatalf("active account's run = %s, want running", got.AggregateStatus)
	}
}
