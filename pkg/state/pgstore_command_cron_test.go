package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
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

func TestPgScheduledCommandCronOccurrencePersistsPolicyDecisions(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID, deploymentID := seedLiveDeploy(t, store, ctx, "command-cron-occurrence-"+uuid.NewString(), "command-occurrence-"+uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/tmp/cron-occurrence.ext4", "apps/cron/occurrence.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "", true, state.CronOptions{
		Command: []string{"bin/maintenance"},
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "skip", StartDeadlineSeconds: 60, MissedRuns: "skip",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firstAt := time.Now().UTC().Truncate(time.Minute)
	first, firstOccurrence, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, nil, firstAt,
		state.CronScheduledOccurrenceOptions{ScheduledFor: firstAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || !created || first.OccurrenceID != firstOccurrence.ID || firstOccurrence.Status != "queued" {
		t.Fatalf("first occurrence = %+v / %+v, created=%t, err=%v", first, firstOccurrence, created, err)
	}
	secondAt := firstAt.Add(time.Minute)
	_, overlap, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, &firstAt, secondAt,
		state.CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || created || overlap.Status != "skipped_overlap" || overlap.BlockingOccurrenceID != firstOccurrence.ID {
		t.Fatalf("overlap occurrence = %+v, created=%t, err=%v", overlap, created, err)
	}
	thirdAt := secondAt.Add(time.Minute)
	_, late, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, &secondAt, thirdAt.Add(2*time.Minute),
		state.CronScheduledOccurrenceOptions{ScheduledFor: thirdAt, ScheduleRevision: cron.ScheduleRevision})
	if err != nil || created || late.Status != "missed_deadline" {
		t.Fatalf("late occurrence = %+v, created=%t, err=%v", late, created, err)
	}
	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 3 || history[0].Status != "missed_deadline" || history[1].Status != "skipped_overlap" || history[2].Status != "queued" {
		t.Fatalf("occurrence history = %+v, %v", history, err)
	}
}

func TestPgScheduledHTTPCronOccurrenceLinksInvocationAndEnforcesFirstStartDeadline(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, store, ctx, "http-cron-policy-"+uuid.NewString(), "http-cron-policy-"+uuid.NewString()[:8])
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "/sync", true, state.CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "skip", StartDeadlineSeconds: 10, MissedRuns: "skip",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	scheduledFor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	evaluatedAt := scheduledFor.Add(5 * time.Second)
	invocation, occurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, evaluatedAt,
		state.CronScheduledOccurrenceOptions{ScheduledFor: scheduledFor, ScheduleRevision: cron.ScheduleRevision}, state.Invocation{
			Method: "POST", Path: "/sync", Headers: []byte(`{"x-faas-cron":"true"}`),
		})
	if err != nil || !created || occurrence.Status != "queued" || occurrence.InvocationID != invocation.ID || invocation.OccurrenceID != occurrence.ID {
		t.Fatalf("scheduled HTTP occurrence = invocation %+v, occurrence %+v, created=%t, err=%v", invocation, occurrence, created, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, invocation.ID, "", 60, 10); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("late first claim = %v; want ErrNotFound", err)
	}
	expired, err := store.ExpireUnstartedScheduledCronInvocations(ctx, time.Now().UTC(), 10)
	if err != nil || expired != 1 {
		t.Fatalf("expire scheduled invocation = %d, %v; want one", expired, err)
	}
	stored, err := store.InvocationByID(ctx, invocation.ID)
	if err != nil || stored.State != state.InvocationFailed || stored.Outcome == nil || *stored.Outcome != state.OutcomeTimeout {
		t.Fatalf("expired invocation = %+v, %v; want timeout", stored, err)
	}
	history, err := store.ScheduleOccurrenceListByCron(ctx, cron.ID, 10, "")
	if err != nil || len(history) != 1 || history[0].Status != "missed_deadline" || history[0].InvocationID != invocation.ID {
		t.Fatalf("expired occurrence history = %+v, %v; want linked missed_deadline", history, err)
	}

	retryCron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "/retry", true, state.CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "allow", StartDeadlineSeconds: 120, MissedRuns: "skip",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions(retry): %v", err)
	}
	retryScheduledAt := time.Now().UTC().Truncate(time.Minute)
	retryInvocation, retryOccurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, retryCron.ID, nil, retryScheduledAt,
		state.CronScheduledOccurrenceOptions{ScheduledFor: retryScheduledAt, ScheduleRevision: retryCron.ScheduleRevision}, state.Invocation{Method: "POST", Path: "/retry"})
	if err != nil || !created || retryOccurrence.Status != "queued" {
		t.Fatalf("retry occurrence = %+v, created=%t, err=%v", retryOccurrence, created, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, retryInvocation.ID, "", 60, 10); err != nil {
		t.Fatalf("first retryable claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `update invocations set start_deadline_at = $2 where id = $1::uuid`, retryInvocation.ID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatalf("expire deadline after first start: %v", err)
	}
	if err := store.FailInvocation(ctx, retryInvocation.ID, "transient", time.Nanosecond, 0); err != nil {
		t.Fatalf("requeue after first attempt: %v", err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, retryInvocation.ID, "", 60, 10); err != nil {
		t.Fatalf("retry claim after first-start deadline: %v; deadline must not block a retry", err)
	}
}

func TestPgScheduledHTTPCronReplaceWaitsForPreviouslyStartedRetry(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, store, ctx, "http-cron-replace-"+uuid.NewString(), "http-cron-replace-"+uuid.NewString()[:8])
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "/sync", true, state.CronOptions{
		SchedulePolicy: &workpolicy.SchedulePolicy{
			Version: workpolicy.Version, Overlap: "replace", MissedRuns: "skip",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firstAt := time.Now().UTC().Truncate(time.Minute)
	first, _, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, nil, firstAt,
		state.CronScheduledOccurrenceOptions{ScheduledFor: firstAt, ScheduleRevision: cron.ScheduleRevision}, state.Invocation{Method: "POST", Path: "/sync"})
	if err != nil || !created {
		t.Fatalf("first occurrence created=%t, err=%v", created, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, first.ID, "", 60, 10); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := store.FailInvocation(ctx, first.ID, "transient", time.Nanosecond, 0); err != nil {
		t.Fatalf("requeue after first attempt: %v", err)
	}
	secondAt := firstAt.Add(time.Minute)
	_, occurrence, created, err := store.CreateScheduledCronInvocationOccurrence(ctx, cron.ID, &firstAt, secondAt,
		state.CronScheduledOccurrenceOptions{ScheduledFor: secondAt, ScheduleRevision: cron.ScheduleRevision}, state.Invocation{Method: "POST", Path: "/sync"})
	if err != nil || created || occurrence.ID != "" {
		t.Fatalf("replacement after earlier start = %+v, created=%t, err=%v; must wait", occurrence, created, err)
	}
	retried, err := store.InvocationByID(ctx, first.ID)
	if err != nil || retried.State != state.InvocationPending || retried.ReceivedAt == nil {
		t.Fatalf("requeued invocation = %+v, %v; want pending with first-start evidence", retried, err)
	}
	storedCron, err := store.CronByID(ctx, cron.ID)
	if err != nil || !storedCron.LastFiredAt.Equal(firstAt) {
		t.Fatalf("cron cursor = %v, %v; blocked replacement must leave it at %v", storedCron.LastFiredAt, err, firstAt)
	}
}

func TestPgCommandCronClassifiesStructuredOutcomeFromSuccessfulExit(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID, deploymentID := seedLiveDeploy(t, store, ctx, "classified-command-cron-"+uuid.NewString(), "classified-cron-"+uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/tmp/classified-cron.ext4", "apps/classified/rootfs.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "", true, state.CronOptions{
		Command: []string{"bin/synchronize"}, RetryMax: 2, RetryBackoffSeconds: 1,
		FailureRules: &workpolicy.FailureRules{
			Version:          workpolicy.Version,
			Rules:            []workpolicy.FailureRule{{OutcomeCodes: []string{"invalid_record"}, Action: "fail_partition"}},
			UnmatchedFailure: "retry", UncertainOutcome: "hold",
		},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	firedAt := time.Now().UTC().Truncate(time.Minute)
	task, created, err := store.CreateScheduledCronAppTask(ctx, cron.ID, nil, firedAt)
	if err != nil || !created {
		t.Fatalf("CreateScheduledCronAppTask = %+v, created=%t, err=%v", task, created, err)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "classified-cron-worker", firedAt.Add(time.Second), time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("ClaimNextAppTask = %+v, err %v", claimed, err)
	}
	running, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, firedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}
	exitCode := 0
	completed, err := store.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *running.LeaseToken, Status: state.AppTaskSucceeded,
		ExitCode: &exitCode, OutcomeCode: "invalid_record", FinishedAt: firedAt.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("CompleteAppTask: %v", err)
	}
	if completed.Status != state.AppTaskFailed || completed.RetryAt != nil || completed.OutcomeCode != "invalid_record" ||
		completed.FailureCode == nil || *completed.FailureCode != "classified_outcome" || completed.WorkDecision == nil ||
		completed.WorkDecision.Classification != "permanent" || completed.WorkDecision.Action != "fail_partition" {
		t.Fatalf("classified command outcome = %+v; want stored terminal permanent failure", completed)
	}
}

func TestPgCommandCronReaperHonorsUncertainOutcomePolicy(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID, deploymentID := seedLiveDeploy(t, store, ctx, "uncertain-command-cron-"+uuid.NewString(), "uncertain-cron-"+uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/tmp/uncertain-cron.ext4", "apps/cron/uncertain.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	base := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Minute)
	for i, uncertainOutcome := range []string{"hold", "retry"} {
		cron, err := store.CreateCronWithOptions(ctx, appID, "* * * * *", "", true, state.CronOptions{
			// The app/schedule/path/command tuple is unique; make the two
			// policy fixtures distinct while exercising the same behavior.
			Command: []string{"bin/synchronize", uncertainOutcome}, RetryMax: 1, RetryBackoffSeconds: 1,
			FailureRules: &workpolicy.FailureRules{
				Version: workpolicy.Version, UnmatchedFailure: "retry", UncertainOutcome: uncertainOutcome,
			},
		})
		if err != nil {
			t.Fatalf("CreateCronWithOptions(%s): %v", uncertainOutcome, err)
		}
		firedAt := base.Add(time.Duration(i) * time.Minute)
		task, _, created, err := store.CreateScheduledCronAppTaskOccurrence(ctx, cron.ID, nil, firedAt,
			state.CronScheduledOccurrenceOptions{ScheduledFor: firedAt, ScheduleRevision: cron.ScheduleRevision})
		if err != nil || !created {
			t.Fatalf("CreateScheduledCronAppTaskOccurrence(%s) = %+v, %t, %v", uncertainOutcome, task, created, err)
		}
		claimedAt := firedAt.Add(time.Second)
		claimed, err := store.ClaimNextAppTask(ctx, "cron-worker-"+uncertainOutcome, claimedAt, time.Second)
		if err != nil || claimed.ID != task.ID {
			t.Fatalf("ClaimNextAppTask(%s) = %+v, %v", uncertainOutcome, claimed, err)
		}
		if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, claimedAt.Add(100*time.Millisecond)); err != nil {
			t.Fatalf("MarkAppTaskRunning(%s): %v", uncertainOutcome, err)
		}
		sweep, err := store.SweepExpiredAppTasks(ctx, claimedAt.Add(2*time.Second))
		if err != nil {
			t.Fatalf("SweepExpiredAppTasks(%s): %v", uncertainOutcome, err)
		}
		recovered, err := store.AppTaskByID(ctx, task.AccountID, appID, task.ID)
		if err != nil || recovered.WorkDecision == nil || recovered.WorkDecision.Classification != "uncertain" || recovered.WorkDecision.Action != uncertainOutcome {
			t.Fatalf("expired task(%s) = %+v, %v; want recorded uncertain decision", uncertainOutcome, recovered, err)
		}
		if uncertainOutcome == "hold" && (recovered.Status != state.AppTaskFailed || sweep.FailedRuns != 1) {
			t.Fatalf("hold result = task %+v, sweep %+v; want terminal failed run", recovered, sweep)
		}
		if uncertainOutcome == "retry" && (recovered.Status != state.AppTaskQueued || recovered.RetryAt == nil || sweep.FailedRuns != 0) {
			t.Fatalf("retry result = task %+v, sweep %+v; want bounded retry queued", recovered, sweep)
		}
	}
}

func TestPgManualCommandCronFireNowQueuesTaskWithoutMovingCursor(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID, deploymentID := seedLiveDeploy(t, store, ctx, "manual-command-cron-"+uuid.NewString(), "manual-command-"+uuid.NewString()[:8])
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/tmp/manual-cron.ext4", "apps/manual-cron/rootfs.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := store.CreateCronWithOptions(ctx, appID, "0 0 1 1 *", "", true, state.CronOptions{
		Command: []string{"bin/maintenance", "--compact"}, RetryMax: 2, RetryBackoffSeconds: 30,
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	cursor := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	if err := store.MarkCronFired(ctx, cron.ID, cursor); err != nil {
		t.Fatalf("set schedule cursor: %v", err)
	}
	requestID, err := store.InsertFireNowRequest(ctx, cron.ID, accountID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}
	claimed, err := store.ClaimPendingFireNowRequest(ctx)
	if err != nil || claimed.ID != requestID || claimed.Status != state.FireNowStatusRunning {
		t.Fatalf("ClaimPendingFireNowRequest = %+v, %v; want running request %s", claimed, err, requestID)
	}

	firedAt := time.Now().UTC()
	task, err := store.CreateManualCronAppTaskForFireNow(ctx, requestID, firedAt)
	if err != nil {
		t.Fatalf("CreateManualCronAppTaskForFireNow: %v", err)
	}
	if task.CronID != cron.ID || task.Kind != state.AppTaskKindCron || task.DeploymentID != deploymentID ||
		task.ScheduledFor != nil || task.RetryMax != 2 || task.RetryBackoffSeconds != 30 || len(task.Command) != 2 {
		t.Fatalf("manual command task = %+v; want cron settings and no scheduled_for", task)
	}
	request, err := store.GetFireNowRequest(ctx, requestID)
	if err != nil || request.Status != state.FireNowStatusSucceeded || request.TaskID == nil || *request.TaskID != task.ID || request.InvocationID != nil {
		t.Fatalf("fire-now request = %+v, %v; want successful task receipt", request, err)
	}
	// If the scheduler lost its response after commit and retried the operation,
	// it must resolve the original task instead of enqueuing a duplicate.
	replayed, err := store.CreateManualCronAppTaskForFireNow(ctx, requestID, firedAt.Add(time.Second))
	if err != nil || replayed.ID != task.ID {
		t.Fatalf("idempotent replay task = %+v, %v; want original %s", replayed, err, task.ID)
	}
	storedCron, err := store.CronByID(ctx, cron.ID)
	if err != nil || !storedCron.LastFiredAt.Equal(cursor) {
		t.Fatalf("cron cursor = %v, %v; want unchanged %v", storedCron.LastFiredAt, err, cursor)
	}
	runs, err := store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(runs) != 1 || runs[0].ID != task.ID {
		t.Fatalf("command cron runs = %+v, %v; want exactly one task", runs, err)
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
