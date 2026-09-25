package sched

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunScheduledJobsTickCreatesOneRunForDueOccurrence(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{
		AccountID:      "scheduled-account",
		Name:           "minute-worker",
		Kind:           "recurring",
		ImageRef:       "ghcr.io/example/worker:v1",
		Command:        []string{"/app/run"},
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 2,
		RetryMax:       1,
		CronSchedule:   "* * * * *",
		CronTimezone:   "UTC",
	}, 10)
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	now := job.CreatedAt.Add(2 * time.Minute).UTC()
	loop := &Loop{
		engine: &Engine{store: store},
		now:    func() time.Time { return now },
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	loop.runScheduledJobsTick(ctx)
	loop.runScheduledJobsTick(ctx)
	runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0)
	if err != nil {
		t.Fatalf("JobRunListByJob: %v", err)
	}
	if len(runs) != 1 || runs[0].TriggerKind != "scheduled" || runs[0].Tasks != 1 {
		t.Fatalf("scheduled runs = %+v; want exactly one one-task scheduled run", runs)
	}
}

func TestRunScheduledJobsTickDoesNotCatchUpWhilePaused(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{
		AccountID:      "scheduled-account",
		Name:           "minute-worker",
		Kind:           "recurring",
		ImageRef:       "ghcr.io/example/worker:v1",
		RAMMB:          256,
		TaskTimeoutS:   60,
		MaxParallelism: 1,
		CronSchedule:   "* * * * *",
		CronTimezone:   "UTC",
	}, 10)
	if err != nil {
		t.Fatalf("JobCreateScheduledIfUnderQuota: %v", err)
	}
	paused := "paused"
	if _, err := store.JobUpdate(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, &paused); err != nil {
		t.Fatalf("JobUpdate(pause): %v", err)
	}
	loop := &Loop{
		engine: &Engine{store: store},
		now:    func() time.Time { return job.CreatedAt.Add(24 * time.Hour) },
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loop.runScheduledJobsTick(ctx)
	runs, err := store.JobRunListByJob(ctx, job.ID, 10, 0)
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs while paused = %+v, err %v; want none", runs, err)
	}
}
