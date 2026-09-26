package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 099 — the cron cursor guards scheduled command task creation.
func TestPgScheduledCommandCronCreatesCursorGuardedTask(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID, deploymentID := seedLiveDeploy(t, store, ctx, "command-cron-"+uuid.NewString(), "command-cron-"+uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/tmp/cron.ext4", "apps/cron/rootfs.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "/", true, state.CronOptions{
		Command: []string{"bin/maintenance", "--compact"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firedAt := time.Now().UTC().Truncate(time.Minute)
	task, created, err := store.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || !created {
		t.Fatalf("CreateScheduledCronAppTask = %+v, created=%t, err=%v", task, created, err)
	}
	if task.CronID != cron.ID || task.Kind != state.AppTaskKindCron || task.DeploymentID != deploymentID ||
		task.ScheduledFor == nil || !task.ScheduledFor.Equal(firedAt) || len(task.Command) != 2 {
		t.Fatalf("scheduled task did not round-trip cron metadata: %+v", task)
	}

	duplicate, created, err := store.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || created || duplicate.ID != "" {
		t.Fatalf("stale scheduler snapshot = %+v, created=%t, err=%v; want no-op", duplicate, created, err)
	}
	storedCron, err := store.CronByID(ctx, cron.ID)
	if err != nil || !storedCron.LastFiredAt.Equal(firedAt) {
		t.Fatalf("cron cursor = %v, %v; want %v", storedCron.LastFiredAt, err, firedAt)
	}
	if active, err := store.CountActiveCronAppTasks(ctx, cron.ID); err != nil || active != 1 {
		t.Fatalf("CountActiveCronAppTasks = %d, %v; want 1", active, err)
	}
	runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 1 || runs[0].ID != task.ID {
		t.Fatalf("ListCronAppTaskRuns = %+v, %v; want the created task", runs, err)
	}
}

// spec: command cron retries are durable in Postgres and reuse one scheduled occurrence row.
func TestPgScheduledCommandCronRetryReusesOccurrence(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID, deploymentID := seedLiveDeploy(t, store, ctx, "command-cron-retry-"+uuid.NewString(), "command-cron-retry-"+uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/tmp/cron-retry.ext4", "apps/cron/retry.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "/", true, state.CronOptions{
		Command: []string{"bin/maintenance"}, RetryMax: 1, RetryBackoffSeconds: 10,
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firedAt := time.Now().UTC().Truncate(time.Minute)
	task, created, err := store.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || !created {
		t.Fatalf("CreateScheduledCronAppTask = %+v, created=%t, err=%v", task, created, err)
	}
	claimedAt := firedAt.Add(time.Second)
	first, err := store.ClaimNextAppTask(ctx, "retry-schedd-a", claimedAt, time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask(first): %v", err)
	}
	running, err := store.MarkAppTaskRunning(ctx, task.ID, *first.LeaseToken, claimedAt.Add(time.Second))
	if err != nil || running.AttemptCount != 1 {
		t.Fatalf("MarkAppTaskRunning(first) = %+v, %v", running, err)
	}
	failureCode, failureMessage := "command_failed", "temporary outage"
	failedAt := claimedAt.Add(2 * time.Second)
	requeued, err := store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *first.LeaseToken, Status: state.AppTaskFailed,
		FailureCode: &failureCode, FailureMessage: &failureMessage, FinishedAt: failedAt,
	})
	if err != nil {
		t.Fatalf("CompleteAppTask(first failure): %v", err)
	}
	wantRetryAt := failedAt.Add(10 * time.Second)
	if requeued.Status != state.AppTaskQueued || requeued.AttemptCount != 1 || requeued.RetryAt == nil ||
		!requeued.RetryAt.Equal(wantRetryAt) || requeued.FinishedAt != nil {
		t.Fatalf("requeued task = %+v; want same queued occurrence with retry deadline %s", requeued, wantRetryAt)
	}
	if early, err := store.ClaimNextAppTask(ctx, "retry-schedd-b", wantRetryAt.Add(-time.Nanosecond), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("early retry claim = %+v, %v; want ErrNotFound", early, err)
	}
	second, err := store.ClaimNextAppTask(ctx, "retry-schedd-b", wantRetryAt, time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask(retry): %v", err)
	}
	running, err = store.MarkAppTaskRunning(ctx, task.ID, *second.LeaseToken, wantRetryAt.Add(time.Second))
	if err != nil || running.AttemptCount != 2 || running.FailureMessage != nil || running.RetryAt != nil {
		t.Fatalf("MarkAppTaskRunning(retry) = %+v, %v", running, err)
	}
	exitCode := 0
	completed, err := store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *second.LeaseToken, Status: state.AppTaskSucceeded,
		ExitCode: &exitCode, FinishedAt: wantRetryAt.Add(2 * time.Second),
	})
	if err != nil || completed.Status != state.AppTaskSucceeded || completed.AttemptCount != 2 {
		t.Fatalf("CompleteAppTask(success) = %+v, %v", completed, err)
	}
	runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 1 || runs[0].ID != task.ID || runs[0].AttemptCount != 2 {
		t.Fatalf("ListCronAppTaskRuns = %+v, %v; want one logical row with two attempts", runs, err)
	}
}
