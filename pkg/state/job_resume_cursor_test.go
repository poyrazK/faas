package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Resuming a job paused for a week caught up every missed occurrence, about
// 10,000 single-row transactions for an every-minute job, in one scheduler
// tick. Resuming now starts the schedule at the resume time (bug hunt
// 2026-10-28 H65).
func TestResumingAPausedJobStartsItsScheduleNow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{
		AccountID: "account-resume", Name: "every-minute", Kind: "recurring",
		ImageRef: "ghcr.io/example/tick:v1", RAMMB: 256, TaskTimeoutS: 60, MaxParallelism: 1,
		CronSchedule: "* * * * *", CronTimezone: "UTC",
	}, api.JobMaxPerAccount[api.PlanHobby.PlanIndex()])
	if err != nil {
		t.Fatal(err)
	}
	paused, active := "paused", "active"
	if _, err := store.JobUpdateWithSchedule(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, &paused, nil, nil); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	resumed, err := store.JobUpdateWithSchedule(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, &active, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.LastScheduledAt == nil || resumed.LastScheduledAt.Before(before) {
		t.Fatalf("last_scheduled_at after resume = %v, want at or after %v", resumed.LastScheduledAt, before)
	}
}
