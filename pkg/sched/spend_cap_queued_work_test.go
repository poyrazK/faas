// adr: 1162 — a billing subject at its monthly spend cap keeps its queued
// work: invocations wait without spending attempts, and crons and recurring
// jobs stay due instead of queueing one run per scheduled time.
package sched_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// switchableCap reports every subject at its cap while reached is set.
func switchableCap(reached *atomic.Bool) sched.OverageChecker {
	return sched.NewMockChecker(func(context.Context, string) (sched.OverageStatus, int64, int64, error) {
		if reached.Load() {
			return sched.OverageReached, 1_000, 1_000, nil
		}
		return sched.OverageOK, 0, 0, nil
	})
}

// The wake gate refuses a capped subject's wake (HTTP 402). The drain used to
// retry that refusal until the row was dead-lettered; it now defers the row
// without spending an attempt and delivers it once the cap lifts.
func TestDrainDefersInvocationsAtTheSpendCap(t *testing.T) {
	ctx := context.Background()
	d, store, _, _, synth := sched.NewDrainHarness(t, api.PlanHobby, true)
	var reached atomic.Bool
	reached.Store(true)
	sched.DrainEngine(d).WithOverageChecker(switchableCap(&reached))
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: apps[0].ID, AccountID: apps[0].AccountID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/task", DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	before := (*synth.Calls()).Load()
	d.Tick(ctx)
	if calls := (*synth.Calls()).Load() - before; calls != 0 {
		t.Fatalf("deliveries at the spend cap = %d, want 0", calls)
	}
	held, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held.State != state.InvocationPending || held.Attempts != 0 || !held.DueAt.After(time.Now()) {
		t.Fatalf("invocation at the cap = state %s attempts %d due %v; want pending, no attempt spent, deferred", held.State, held.Attempts, held.DueAt)
	}
}

// A cron of an app at its spend cap queues nothing and stays due; it runs once
// after the cap lifts, as after a budget hold.
func TestCronSweepSkipsAppsAtTheSpendCap(t *testing.T) {
	t.Parallel()
	mem := state.NewMemStore()
	acct, _ := mem.CreateAccount(t.Context(), "capped-cron@example.com", api.PlanPro)
	c := hourlyCron(t, mem, acct.ID, "capped-cron", true, state.CronOptions{})
	var reached atomic.Bool
	reached.Store(true)
	loop := func() *sched.Loop {
		eng, _ := sched.MakeEngine(t, mem, &sched.FakeWakeVMM{})
		eng.WithOverageChecker(switchableCap(&reached))
		return sched.NewLoop(nil, eng, slog.Default()).
			WithGatewaySynth(&sched.RecordingSynth{}).
			WithClock(func() time.Time { return cronTestNoon })
	}
	loop().RunCronTick(t.Context())
	if runs := queuedCronRuns(t, mem, acct.ID); len(runs) != 0 {
		t.Fatalf("queued runs at the spend cap = %d, want 0", len(runs))
	}
	if got, _ := mem.CronByID(t.Context(), c.ID); !got.LastFiredAt.IsZero() {
		t.Fatalf("capped cron consumed its occurrence at %v", got.LastFiredAt)
	}
	reached.Store(false)
	loop().RunCronTick(t.Context())
	if runs := queuedCronRuns(t, mem, acct.ID); len(runs) != 1 {
		t.Fatalf("queued runs after the cap lifted = %d, want 1", len(runs))
	}
}

func recurringJob(t *testing.T, store *state.MemStore, accountID string) state.Job {
	t.Helper()
	job, err := store.JobCreateScheduledIfUnderQuota(t.Context(), state.Job{
		AccountID: accountID, Name: "minutely", Kind: "recurring", ImageRef: "ghcr.io/example/worker:v1",
		Command: []string{"/run"}, RAMMB: 256, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1,
		CronSchedule: "* * * * *", CronTimezone: "UTC",
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func scheduledJobRuns(t *testing.T, store *state.MemStore, jobID string) []state.JobRun {
	t.Helper()
	runs, err := store.JobRunListByJob(t.Context(), jobID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// A recurring job of an account at its spend cap, or one whose earlier
// scheduled run has not started, stays due: the dispatcher defers held tasks
// without failing them, so a run per scheduled time piled up and all of them
// ran when the hold lifted.
func TestScheduledJobsStayDueWhileHeld(t *testing.T) {
	t.Parallel()
	mem := state.NewMemStore()
	acct, _ := mem.CreateAccount(t.Context(), "capped-job@example.com", api.PlanPro)
	job := recurringJob(t, mem, acct.ID)
	var reached atomic.Bool
	reached.Store(true)
	now := job.CreatedAt.Add(2 * time.Minute).UTC()
	eng, _ := sched.MakeEngine(t, mem, &sched.FakeWakeVMM{})
	eng.WithOverageChecker(switchableCap(&reached))
	loop := sched.NewLoop(nil, eng, slog.Default()).WithClock(func() time.Time { return now })

	for range 3 {
		loop.RunScheduledJobsTick(t.Context())
		now = now.Add(time.Minute)
	}
	if runs := scheduledJobRuns(t, mem, job.ID); len(runs) != 0 {
		t.Fatalf("runs queued at the spend cap = %d, want 0", len(runs))
	}
	reached.Store(false)
	for range 3 {
		loop.RunScheduledJobsTick(t.Context())
		now = now.Add(time.Minute)
	}
	runs := scheduledJobRuns(t, mem, job.ID)
	if len(runs) != 1 || runs[0].TriggerKind != "scheduled" || runs[0].AggregateStatus != "queued" {
		t.Fatalf("runs after the cap lifted while the first is unstarted = %+v, want one queued run", runs)
	}
}
