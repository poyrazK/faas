package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemStoreScheduledHTTPCronOccurrenceAndOverlapSkip(t *testing.T) {
	store, ctx, account, app, _ := memCoverageFixture(t)
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/sync", true, CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "skip", MissedRuns: "skip"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firstAt := time.Now().UTC().Truncate(time.Minute)
	first, firstOccurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, firstAt,
		CronScheduledOccurrenceOptions{ScheduledFor: firstAt, ScheduleRevision: cron.ScheduleRevision}, Invocation{
			Method: "POST", Path: "/sync", Headers: []byte(`{"x-faas-cron":"true"}`),
		})
	if err != nil || !created || first.ID == "" || first.OccurrenceID != firstOccurrence.ID || firstOccurrence.InvocationID != first.ID {
		t.Fatalf("first HTTP occurrence = invocation %+v, occurrence %+v, created=%t, err=%v", first, firstOccurrence, created, err)
	}
	if first.Source != InvocationCron || first.State != InvocationPending || first.AccountID != account.ID || first.DueAt != firstAt {
		t.Fatalf("queued HTTP invocation lost scheduled metadata: %+v", first)
	}
	secondAt := firstAt.Add(time.Minute)
	_, skipped, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, &firstAt, secondAt,
		CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision}, Invocation{})
	if err != nil || created || skipped.Status != "skipped_overlap" || skipped.BlockingOccurrenceID != firstOccurrence.ID {
		t.Fatalf("overlap occurrence = %+v, created=%t, err=%v; want an explained skip", skipped, created, err)
	}
	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 2 || history[0].Status != "skipped_overlap" || history[1].Status != "queued" {
		t.Fatalf("occurrence history = %+v, %v; want skipped and queued", history, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, first.ID, "", 60, 10); err != nil {
		t.Fatalf("claim queued HTTP cron invocation: %v", err)
	}
	if err := store.CompleteInvocation(ctx, first.ID, nil); err != nil {
		t.Fatalf("complete HTTP cron invocation: %v", err)
	}
	history, err = store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || history[1].Status != "succeeded" || history[1].StartedAt == nil || history[1].FinishedAt == nil {
		t.Fatalf("completed HTTP occurrence = %+v, %v; want lifecycle timestamps", history[1], err)
	}
	if _, _, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, secondAt.Add(time.Minute),
		CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision}, Invocation{}); err != nil || created {
		t.Fatalf("stale duplicate occurrence created=%t, err=%v; want compare-and-set no-op", created, err)
	}
}

func TestMemStoreScheduledHTTPCronReplaceCancelsOnlyUnstartedWork(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/sync", true, CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "replace", MissedRuns: "skip"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firstAt := time.Now().UTC().Truncate(time.Minute)
	first, firstOccurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, firstAt,
		CronScheduledOccurrenceOptions{ScheduledFor: firstAt, ScheduleRevision: cron.ScheduleRevision}, Invocation{Method: "POST", Path: "/sync"})
	if err != nil || !created {
		t.Fatalf("first occurrence created=%t, err=%v", created, err)
	}
	secondAt := firstAt.Add(time.Minute)
	second, secondOccurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, &firstAt, secondAt,
		CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision}, Invocation{Method: "POST", Path: "/sync"})
	if err != nil || !created || second.ID == "" || secondOccurrence.Status != "queued" {
		t.Fatalf("replacement occurrence = %+v, created=%t, err=%v", secondOccurrence, created, err)
	}
	old, err := store.InvocationByID(ctx, first.ID)
	if err != nil || old.State != InvocationCancelled {
		t.Fatalf("replaced invocation = %+v, %v; want cancelled", old, err)
	}
	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 2 || history[0].ID != secondOccurrence.ID || history[1].ID != firstOccurrence.ID ||
		history[1].Status != "cancelled" || history[1].Reason == "" {
		t.Fatalf("replacement history = %+v, %v; want queued and explained cancellation", history, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, second.ID, "", 60, 10); err != nil {
		t.Fatalf("claim replacement: %v", err)
	}
	thirdAt := secondAt.Add(time.Minute)
	_, _, created, err = store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, &secondAt, thirdAt,
		CronScheduledOccurrenceOptions{ScheduledFor: thirdAt, ScheduleRevision: cron.ScheduleRevision}, Invocation{})
	if err != nil || created {
		t.Fatalf("replacement while dispatching created=%t, err=%v; must wait for confirmed stop", created, err)
	}
	if count, err := store.RequeueExpiredInvocations(ctx, time.Now().UTC().Add(2*time.Minute), 10); err != nil || count != 1 {
		t.Fatalf("requeue started invocation = %d, %v; want one retryable invocation", count, err)
	}
	_, _, created, err = store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, &secondAt, thirdAt,
		CronScheduledOccurrenceOptions{ScheduledFor: thirdAt, ScheduleRevision: cron.ScheduleRevision}, Invocation{Method: "POST", Path: "/sync"})
	if err != nil || created {
		t.Fatalf("replacement while retry is pending created=%t, err=%v; must wait for already-started work", created, err)
	}
	retried, err := store.InvocationByID(ctx, second.ID)
	if err != nil || retried.State != InvocationPending || retried.ReceivedAt == nil {
		t.Fatalf("requeued invocation = %+v, %v; want pending with first-start evidence", retried, err)
	}
	storedCron, err := store.CronByID(ctx, cron.ID)
	if err != nil || !storedCron.LastFiredAt.Equal(secondAt) {
		t.Fatalf("cron cursor = %v, %v; blocked replacement must leave it at %v", storedCron.LastFiredAt, err, secondAt)
	}
}

func TestMemStoreScheduledHTTPCronDeadlineRejectsLateFirstStartAndExpires(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/sync", true, CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "allow", StartDeadlineSeconds: 10, MissedRuns: "skip",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	scheduledFor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	evaluatedAt := scheduledFor.Add(5 * time.Second)
	invocation, occurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, evaluatedAt,
		CronScheduledOccurrenceOptions{ScheduledFor: scheduledFor, ScheduleRevision: cron.ScheduleRevision}, Invocation{Method: "POST", Path: "/sync"})
	if err != nil || !created || occurrence.Status != "queued" {
		t.Fatalf("deadline occurrence = %+v, created=%t, err=%v", occurrence, created, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, invocation.ID, "", 60, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late first claim = %v; want ErrNotFound", err)
	}
	expired, err := store.ExpireUnstartedScheduledCronInvocations(ctx, time.Now().UTC(), 10)
	if err != nil || expired != 1 {
		t.Fatalf("expire unstarted invocation = %d, %v; want one", expired, err)
	}
	got, err := store.InvocationByID(ctx, invocation.ID)
	if err != nil || got.State != InvocationFailed || got.Outcome == nil || *got.Outcome != OutcomeTimeout {
		t.Fatalf("expired invocation = %+v, %v; want timeout failure", got, err)
	}
	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 1 || history[0].Status != "missed_deadline" {
		t.Fatalf("expired occurrence history = %+v, %v; want missed_deadline", history, err)
	}
}

func TestMemStoreScheduledHTTPCronRetrySurvivesFirstStartDeadline(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "/retry", true, CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "allow", StartDeadlineSeconds: 120, MissedRuns: "skip",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	scheduledFor := time.Now().UTC().Truncate(time.Minute)
	invocation, _, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, scheduledFor,
		CronScheduledOccurrenceOptions{ScheduledFor: scheduledFor, ScheduleRevision: cron.ScheduleRevision}, Invocation{Method: "POST", Path: "/retry"})
	if err != nil || !created {
		t.Fatalf("scheduled occurrence created=%t, err=%v", created, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, invocation.ID, "", 60, 10); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	store.mu.Lock()
	started := store.invocations[invocation.ID]
	if started.ReceivedAt == nil {
		store.mu.Unlock()
		t.Fatal("first claim did not persist received_at")
	}
	lateDeadline := time.Now().UTC().Add(-time.Second)
	started.StartDeadlineAt = &lateDeadline
	store.invocations[invocation.ID] = started
	store.mu.Unlock()
	if err := store.FailInvocation(ctx, invocation.ID, "transient", time.Nanosecond, 0); err != nil {
		t.Fatalf("requeue after first attempt: %v", err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, invocation.ID, "", 60, 10); err != nil {
		t.Fatalf("retry claim after first-start deadline: %v; deadline must not block a retry", err)
	}
}
