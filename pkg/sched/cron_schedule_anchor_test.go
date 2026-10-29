// adr: 20261026201726
package sched_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// cronInvocations counts the account's queued and settled cron invocations.
func cronInvocations(t *testing.T, store state.Store, accountID string) int {
	t.Helper()
	rows, err := store.ListInvocationsForAccount(context.Background(), accountID, 100, "")
	if err != nil {
		t.Fatalf("ListInvocationsForAccount: %v", err)
	}
	n := 0
	for _, row := range rows {
		if row.Source == state.InvocationCron {
			n++
		}
	}
	return n
}

// anchoredTick ticks the cron loop at the cron's schedule anchor and at the
// first boundary after it, and returns the cron invocations queued at each.
func anchoredTick(t *testing.T, store *state.MemStore, accountID string, cron state.Cron) (atAnchor, atBoundary int) {
	t.Helper()
	if cron.ScheduleAnchorAt.IsZero() {
		t.Fatalf("cron %s has no schedule anchor", cron.ID)
	}
	schedule, err := sched.ParseScheduleWithTimezone(cron.Schedule, cron.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	eng, _ := sched.MakeEngine(t, store, &sched.FakeWakeVMM{})
	clock := cron.ScheduleAnchorAt
	loop := sched.NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(&sched.RecordingSynth{}).
		WithClock(func() time.Time { return clock })
	before := cronInvocations(t, store, accountID)
	loop.RunCronTick(t.Context())
	atAnchor = cronInvocations(t, store, accountID) - before
	clock = schedule.NextFireAt(cron.ScheduleAnchorAt)
	loop.RunCronTick(t.Context())
	atBoundary = cronInvocations(t, store, accountID) - before - atAnchor
	return atAnchor, atBoundary
}

// Moving a nightly cron used to fire it at once: the next boundary was
// computed from the last fire under the new schedule, which had passed.
func TestCronScheduleEditWaitsForNextBoundary(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := t.Context()
	acct, _ := store.CreateAccount(ctx, "cron-edit@example.com", api.PlanHobby)
	_, cron := sched.NewAppAndCron(t, store, acct.ID, true)
	if err := store.MarkCronFired(ctx, cron.ID, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	every5 := "*/5 * * * *"
	edited, err := store.UpdateCronWithOptions(ctx, cron.ID, &every5, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	atAnchor, atBoundary := anchoredTick(t, store, acct.ID, edited)
	if atAnchor != 0 || atBoundary != 1 {
		t.Fatalf("fires at edit = %d, at next boundary = %d; want 0 and 1", atAnchor, atBoundary)
	}
}

func TestCronReenableWaitsForNextBoundary(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := t.Context()
	acct, _ := store.CreateAccount(ctx, "cron-reenable@example.com", api.PlanHobby)
	_, cron := sched.NewAppAndCron(t, store, acct.ID, true)
	if err := store.MarkCronFired(ctx, cron.ID, time.Now().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	off, on := false, true
	if _, err := store.UpdateCronWithOptions(ctx, cron.ID, nil, nil, &off, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	enabled, err := store.UpdateCronWithOptions(ctx, cron.ID, nil, nil, &on, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	atAnchor, atBoundary := anchoredTick(t, store, acct.ID, enabled)
	if atAnchor != 0 || atBoundary != 1 {
		t.Fatalf("fires at enable = %d, at next boundary = %d; want 0 and 1", atAnchor, atBoundary)
	}
}

// A policy cron suspended for a week used to record one missed occurrence
// per passed minute (up to 10,000) on the scheduler loop when a redeploy
// reactivated it.
func TestCronReactivationDoesNotReplayPassedOccurrences(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := t.Context()
	acct, _ := store.CreateAccount(ctx, "cron-reactivate@example.com", api.PlanPro)
	app, cron := sched.NewAppAndCron(t, store, acct.ID, true)
	policy := &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", MissedRuns: "skip"}
	if _, err := store.UpdateCronWithOptions(ctx, cron.ID, nil, nil, nil, nil, nil, nil, state.CronOptions{SchedulePolicy: policy}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCronFired(ctx, cron.ID, time.Now().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SuspendCronsForApp(ctx, app.ID, state.CronSuspendedNoLiveDeployment); err != nil {
		t.Fatal(err)
	}
	if n, err := store.ReactivateCronsForApp(ctx, app.ID); err != nil || n != 1 {
		t.Fatalf("ReactivateCronsForApp = %d, %v", n, err)
	}
	reactivated, err := store.CronByID(ctx, cron.ID)
	if err != nil {
		t.Fatal(err)
	}
	atAnchor, atBoundary := anchoredTick(t, store, acct.ID, reactivated)
	if atAnchor != 0 || atBoundary != 1 {
		t.Fatalf("fires at reactivation = %d, at next boundary = %d; want 0 and 1", atAnchor, atBoundary)
	}
	occurrences, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 200, "")
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = %d (err %v), want only the first boundary after reactivation", len(occurrences), err)
	}
}

// A plain edit of the path is not a schedule change: the cursor stays.
func TestCronPathEditKeepsScheduleCursor(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := t.Context()
	acct, _ := store.CreateAccount(ctx, "cron-path@example.com", api.PlanHobby)
	_, cron := sched.NewAppAndCron(t, store, acct.ID, true)
	path := "/other"
	edited, err := store.UpdateCronWithOptions(ctx, cron.ID, nil, &path, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !edited.ScheduleAnchorAt.IsZero() {
		t.Fatalf("path edit anchored the schedule at %v", edited.ScheduleAnchorAt)
	}
}

// ADR-20261026201726: the tick queues every due HTTP cron without waiting on
// any app; slow handlers cannot hold the scheduler loop.
func TestCronTickQueuesWithoutDelivering(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := t.Context()
	acct, _ := store.CreateAccount(ctx, "cron-queue@example.com", api.PlanPro)
	created := time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC)
	for i := range 5 {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "queue-" + string(rune('a'+i)), Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage}); err != nil {
			t.Fatal(err)
		}
		c, err := store.CreateCron(ctx, app.ID, "0 * * * *", "/hourly", true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateCron(ctx, c.ID, nil, nil, nil, &created); err != nil {
			t.Fatal(err)
		}
	}
	vmm := &sched.FakeWakeVMM{}
	eng, _ := sched.MakeEngine(t, store, vmm)
	synth := &sched.RecordingSynth{}
	loop := sched.NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithClock(func() time.Time { return time.Date(2026, 7, 17, 12, 0, 1, 0, time.UTC) })
	loop.RunCronTick(ctx)
	if got := cronInvocations(t, store, acct.ID); got != 5 {
		t.Fatalf("queued cron invocations = %d, want 5", got)
	}
	if calls := (*synth.Calls()).Load(); calls != 0 {
		t.Fatalf("gateway calls during the tick = %d, want 0", calls)
	}
	sched.DeliverCronInvocations(t, store, eng, synth)
	if calls := (*synth.Calls()).Load(); calls != 5 {
		t.Fatalf("drain deliveries = %d, want 5", calls)
	}
}
