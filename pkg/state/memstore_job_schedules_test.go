package state

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemStoreJobRunCreateScheduledClaimsOnce(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, Job{
		AccountID:      "account-scheduled",
		Name:           "nightly-export",
		Kind:           "recurring",
		ImageRef:       "ghcr.io/example/exporter:v1",
		Command:        []string{"/app/export"},
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 1,
		RetryMax:       1,
		CronSchedule:   "0 3 * * *",
		CronTimezone:   "Europe/Istanbul",
	}, api.JobMaxPerAccount[api.PlanHobby.PlanIndex()])
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	if job.CronTimezone != "Europe/Istanbul" || job.CronSchedule != "0 3 * * *" {
		t.Fatalf("created schedule = %q %q", job.CronSchedule, job.CronTimezone)
	}

	firedAt := job.CreatedAt.Add(time.Minute).UTC()
	run, created, err := store.JobRunCreateScheduled(ctx, job.ID, job.CronSchedule, job.CronTimezone, nil, firedAt)
	if err != nil || !created {
		t.Fatalf("JobRunCreateScheduled = created %v, err %v; want created run", created, err)
	}
	if run.TriggerKind != "scheduled" || run.Tasks != 1 || run.AggregateStatus != "queued" {
		t.Fatalf("scheduled run = %+v", run)
	}
	if _, created, err := store.JobRunCreateScheduled(ctx, job.ID, job.CronSchedule, job.CronTimezone, nil, firedAt.Add(time.Second)); err != nil || created {
		t.Fatalf("duplicate JobRunCreateScheduled = created %v, err %v; want no-op", created, err)
	}

	updated, err := store.JobGetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("JobGetByID: %v", err)
	}
	if updated.LastScheduledAt == nil || !updated.LastScheduledAt.Equal(firedAt) {
		t.Fatalf("last_scheduled_at = %v, want %v", updated.LastScheduledAt, firedAt)
	}
	runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("JobRunListByJob = %+v, err %v; want one run", runs, err)
	}
}

func TestMemStoreScheduledOccurrenceIsConcurrencySafe(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, Job{
		AccountID:      "account-scheduled-concurrent",
		Name:           "minute-export",
		Kind:           "recurring",
		ImageRef:       "ghcr.io/example/exporter:v1",
		Command:        []string{"/app/export"},
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 1,
		CronSchedule:   "* * * * *",
		CronTimezone:   "UTC",
	}, api.JobMaxPerAccount[api.PlanHobby.PlanIndex()])
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	firedAt := job.CreatedAt.Add(time.Minute).UTC()

	var wg sync.WaitGroup
	created := make(chan bool, 16)
	for i := 0; i < cap(created); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, didCreate, err := store.JobRunCreateScheduled(ctx, job.ID, job.CronSchedule,
				job.CronTimezone, nil, firedAt)
			if err != nil {
				t.Errorf("JobRunCreateScheduled: %v", err)
				return
			}
			created <- didCreate
		}()
	}
	wg.Wait()
	close(created)
	createdCount := 0
	for didCreate := range created {
		if didCreate {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent create count = %d, want exactly one", createdCount)
	}
	runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0)
	if err != nil || len(runs) != 1 {
		t.Fatalf("JobRunListByJob = %d runs, err %v; want one", len(runs), err)
	}
}

func TestMemStoreScheduledJobRunRejectsStaleDefinition(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, Job{
		AccountID:      "account-scheduled",
		Name:           "nightly-export",
		Kind:           "recurring",
		ImageRef:       "ghcr.io/example/exporter:v1",
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 1,
		CronSchedule:   "0 3 * * *",
		CronTimezone:   "UTC",
	}, api.JobMaxPerAccount[api.PlanHobby.PlanIndex()])
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}

	newSchedule := "0 4 * * *"
	if _, err := store.JobUpdateWithSchedule(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, nil, &newSchedule, nil); err != nil {
		t.Fatalf("JobUpdateWithSchedule: %v", err)
	}
	if _, created, err := store.JobRunCreateScheduled(ctx, job.ID, job.CronSchedule, job.CronTimezone, nil, time.Now().UTC()); err != nil || created {
		t.Fatalf("stale JobRunCreateScheduled = created %v, err %v; want no-op", created, err)
	}
	if runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0); err != nil || len(runs) != 0 {
		t.Fatalf("JobRunListByJob after stale candidate = %+v, err %v; want no runs", runs, err)
	}
	empty := ""
	unscheduled, err := store.JobUpdateWithSchedule(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, nil, &empty, nil)
	if err != nil {
		t.Fatalf("JobUpdateWithSchedule(unschedule): %v", err)
	}
	if unscheduled.CronSchedule != "" || unscheduled.Kind != "batch" {
		t.Fatalf("unscheduled job = kind %q schedule %q", unscheduled.Kind, unscheduled.CronSchedule)
	}
}

func TestMemStoreScheduledOccurrenceExpiresBeforeFirstTaskStart(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, Job{
		AccountID: "account-deadline", Name: "deadline-worker", Kind: "recurring",
		ImageRef: "ghcr.io/example/worker:v1", Command: []string{"/app/run"},
		RAMMB: 256, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1,
		CronSchedule: "* * * * *", CronTimezone: "UTC",
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", StartDeadlineSeconds: 60, MissedRuns: "skip"},
	}, api.JobMaxPerAccount[api.PlanHobby.PlanIndex()])
	if err != nil {
		t.Fatalf("create scheduled job: %v", err)
	}
	firedAt := job.CreatedAt.Add(time.Minute).UTC()
	run, created, err := store.JobRunCreateScheduledOccurrence(ctx, job.ID, job.CronSchedule, job.CronTimezone,
		nil, firedAt, JobScheduledOccurrenceOptions{ScheduledFor: firedAt, ScheduleRevision: job.ScheduleRevision})
	if err != nil || !created {
		t.Fatalf("create scheduled run=%+v created=%t err=%v", run, created, err)
	}
	// Move the persisted deadline to the past to exercise the dispatcher expiry
	// path without a wall-clock sleep.
	deadline := time.Now().UTC().Add(-time.Second)
	store.mu.Lock()
	storedRun := store.jobRuns[run.ID]
	storedRun.StartDeadlineAt = &deadline
	store.jobRuns[run.ID] = storedRun
	occurrence := store.scheduleOccurrences[run.OccurrenceID]
	occurrence.StartDeadlineAt = &deadline
	store.scheduleOccurrences[run.OccurrenceID] = occurrence
	store.mu.Unlock()

	expired, err := store.JobTaskExpireUnstarted(ctx, time.Now().UTC())
	if err != nil || len(expired) != 1 || expired[0] != run.ID {
		t.Fatalf("expire unstarted = %v, err=%v; want run %s", expired, err, run.ID)
	}
	if _, err := store.JobRunRecompute(ctx, run.ID); err != nil {
		t.Fatalf("recompute expired run: %v", err)
	}
	rows, err := store.ScheduleOccurrenceListByJob(ctx, job.ID, 10, "")
	if err != nil || len(rows) != 1 || rows[0].Status != "missed_deadline" {
		t.Fatalf("occurrence history = %+v, err=%v; want missed_deadline", rows, err)
	}
	claimed, err := store.JobTaskClaimBatch(ctx, 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("expired task claim batch = %+v, err=%v; want no work", claimed, err)
	}
}
