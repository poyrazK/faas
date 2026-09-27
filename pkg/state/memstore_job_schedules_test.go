package state

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
