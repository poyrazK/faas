package state

import (
	"errors"
	"testing"
	"time"
)

// spec: command cron failures retry the same logical scheduled occurrence with bounded exponential backoff.
func TestMemCommandCronRetriesOneOccurrenceAndKeepsOneRun(t *testing.T) {
	store, ctx, _, app, _ := memCoverageFixture(t)
	deployment, err := store.CreateDeployment(ctx, Deployment{
		AppID: app.ID, ImageDigest: "sha256:retry", Status: DeployLive,
		Kind: DeploymentKindImage, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/retry", "apps/retry.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, app.ID, "* * * * *", "", true, CronOptions{
		Command: []string{"bin/maintenance"}, RetryMax: 1, RetryBackoffSeconds: 10,
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firedAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	task, created, err := store.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || !created {
		t.Fatalf("CreateScheduledCronAppTask = %+v, created=%t, err=%v", task, created, err)
	}
	if task.RetryMax != 1 || task.RetryBackoffSeconds != 10 || task.AttemptCount != 0 {
		t.Fatalf("scheduled task retry policy = %+v", task)
	}

	claimAt := firedAt.Add(time.Second)
	first, err := store.ClaimNextAppTask(ctx, "sched-a", claimAt, time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask(first): %v", err)
	}
	running, err := store.MarkAppTaskRunning(ctx, task.ID, *first.LeaseToken, claimAt.Add(time.Second))
	if err != nil || running.AttemptCount != 1 {
		t.Fatalf("MarkAppTaskRunning(first) = %+v, %v", running, err)
	}
	failureCode, failureMessage := "command_failed", "temporary outage"
	failedAt := claimAt.Add(2 * time.Second)
	queued, err := store.CompleteAppTask(ctx, CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *first.LeaseToken, Status: AppTaskFailed,
		FailureCode: &failureCode, FailureMessage: &failureMessage, FinishedAt: failedAt,
	})
	if err != nil {
		t.Fatalf("CompleteAppTask(first failure): %v", err)
	}
	wantRetryAt := failedAt.Add(10 * time.Second)
	if queued.Status != AppTaskQueued || queued.AttemptCount != 1 || queued.RetryAt == nil ||
		!queued.RetryAt.Equal(wantRetryAt) || queued.FinishedAt != nil || queued.FailureMessage == nil || *queued.FailureMessage != failureMessage {
		t.Fatalf("retry queue row = %+v; want one queued occurrence with retry deadline and failure", queued)
	}
	if runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, ""); err != nil || len(runs) != 1 || runs[0].ID != task.ID {
		t.Fatalf("run history while retrying = %+v, %v; want one logical row", runs, err)
	}
	if _, err := store.ClaimNextAppTask(ctx, "sched-b", wantRetryAt.Add(-time.Nanosecond), time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("early retry claim = %v, want ErrNotFound", err)
	}

	second, err := store.ClaimNextAppTask(ctx, "sched-b", wantRetryAt, time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask(retry): %v", err)
	}
	running, err = store.MarkAppTaskRunning(ctx, task.ID, *second.LeaseToken, wantRetryAt.Add(time.Second))
	if err != nil || running.AttemptCount != 2 || running.FailureCode != nil {
		t.Fatalf("MarkAppTaskRunning(retry) = %+v, %v", running, err)
	}
	exitCode := 0
	succeeded, err := store.CompleteAppTask(ctx, CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *second.LeaseToken, Status: AppTaskSucceeded,
		ExitCode: &exitCode, FinishedAt: wantRetryAt.Add(2 * time.Second),
	})
	if err != nil || succeeded.Status != AppTaskSucceeded || succeeded.AttemptCount != 2 || succeeded.RetryAt != nil {
		t.Fatalf("CompleteAppTask(success) = %+v, %v", succeeded, err)
	}
	if runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, ""); err != nil || len(runs) != 1 || runs[0].AttemptCount != 2 {
		t.Fatalf("terminal run history = %+v, %v; want one row with two attempts", runs, err)
	}
}

func TestCronAppTaskRetryAtUsesExponentialBackoff(t *testing.T) {
	firedAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{{attempt: 1, want: 10 * time.Second}, {attempt: 2, want: 20 * time.Second}, {attempt: 3, want: 40 * time.Second}} {
		task := AppTask{Kind: AppTaskKindCron, AttemptCount: test.attempt, RetryMax: 3, RetryBackoffSeconds: 10}
		got := cronAppTaskRetryAt(task, AppTaskRunning, AppTaskFailed, firedAt)
		if got == nil || !got.Equal(firedAt.Add(test.want)) {
			t.Errorf("attempt %d retry_at = %v, want %v", test.attempt, got, firedAt.Add(test.want))
		}
	}
}
