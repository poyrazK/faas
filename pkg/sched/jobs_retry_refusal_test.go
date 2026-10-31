// adr: 385 — a dead-lettered attempt stops a fail-fast run, and a refused
// automatic retry dead-letters its attempt instead of keeping "retry".

package sched_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// failClaimedTask claims task 0 and reports a plain failure (exit 1, no
// failure rules), the shape of most customer job failures.
func failClaimedTask(t *testing.T, store *state.MemStore, accountID, runID string) {
	t.Helper()
	ctx := context.Background()
	const instanceID, leaseToken = "refusal-instance", "refusal-lease"
	if err := store.JobTaskMarkClaimed(ctx, runID, 0, instanceID, leaseToken, time.Now().Add(time.Minute), state.DefaultLocalNodeName); err != nil {
		t.Fatalf("JobTaskMarkClaimed: %v", err)
	}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	if err := e.HandleJobExit(ctx, accountID, runID, 0, 1, "failed", leaseToken); err != nil {
		t.Fatalf("HandleJobExit: %v", err)
	}
}

func assertFailFastSettled(t *testing.T, store *state.MemStore, runID, reason string) {
	t.Helper()
	ctx := context.Background()
	task, err := store.JobTaskGet(ctx, runID, 0)
	if err != nil || task.Status != "failed" || task.WorkDecision == nil || task.WorkDecision.Action != "dead_letter" || task.WorkDecision.Reason != reason {
		t.Fatalf("failed task = %+v (decision %+v), err %v; want failed, dead_letter/%s", task, task.WorkDecision, err, reason)
	}
	attempts, err := store.JobTaskAttemptList(ctx, runID, 0, 10, 0)
	if err != nil || len(attempts) != 1 || attempts[0].WorkDecision == nil || attempts[0].WorkDecision.Action != "dead_letter" {
		t.Fatalf("attempts = %+v, err %v; want one dead_letter attempt", attempts, err)
	}
	for _, index := range []int{1, 2} {
		task, err := store.JobTaskGet(ctx, runID, index)
		if err != nil || task.Status != "cancelled" {
			t.Fatalf("unstarted task %d = %+v, err %v; want cancelled by fail_fast", index, task, err)
		}
	}
	run, err := store.JobRunGetByID(ctx, runID)
	if err != nil || run.AggregateStatus != "dead_letter" || run.DeadLetterCount != 1 || run.TasksCancelled != 2 {
		t.Fatalf("run = %+v, err %v; want dead_letter with 1 dead letter and 2 cancelled", run, err)
	}
}

// With no failure rules a failure classifies as retry, and with retry_max 0
// its only attempt records dead_letter (retry_budget_exhausted). Fail-fast
// read that as "a retry is coming" and ran every remaining task.
func TestHandleJobExitFailFastStopsAfterExhaustedRetry(t *testing.T) {
	store := state.NewMemStore()
	acct, job, _ := sched.SeedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	zero := 0
	run, _, err := store.JobRunCreate(context.Background(), job.ID, acct.ID, "manual", nil, &zero, nil, nil, 3,
		state.JobRunOptions{FailurePolicy: "fail_fast"})
	if err != nil {
		t.Fatal(err)
	}
	failClaimedTask(t, store, acct.ID, run.ID)
	assertFailFastSettled(t, store, run.ID, "retry_budget_exhausted")
}

// The job's image is republished while the run executes, so JobTaskRetry
// refuses the retry (it would run a different image). The attempt kept
// "retry", the run never dead-lettered and fail-fast never stopped it.
func TestHandleJobExitDeadLettersRefusedRetry(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, job, _ := sched.SeedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	one := 1
	run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, &one, nil, nil, 3,
		state.JobRunOptions{FailurePolicy: "fail_fast"})
	if err != nil {
		t.Fatal(err)
	}
	if run.ImageStorageKeySnapshot == "" {
		t.Fatal("run did not snapshot the job image")
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("b", 64), "jobs/"+job.ID+"-v2.ext4", ""); err != nil {
		t.Fatalf("republish job image: %v", err)
	}
	failClaimedTask(t, store, acct.ID, run.ID)
	assertFailFastSettled(t, store, run.ID, "retry_refused")
}

// A retry that went through is not touched by a late settle of the attempt
// before it.
func TestSettleRefusedJobTaskRetryLeavesARequeuedTask(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, job, _ := sched.SeedJobRun(t, store, json.RawMessage(`{}`), json.RawMessage(`{}`))
	one := 1
	run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, &one, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	failClaimedTask(t, store, acct.ID, run.ID)
	task, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || task.Status != "queued" || task.Attempt != 2 {
		t.Fatalf("task = %+v, err %v; want the automatic retry queued as attempt 2", task, err)
	}
	decision := workpolicy.Decision{PolicyVersion: workpolicy.Version, Classification: "retryable", Action: "dead_letter", Reason: "retry_refused"}
	if settled, err := store.SettleRefusedJobTaskRetry(ctx, run.ID, 0, 1, decision); err != nil || settled {
		t.Fatalf("settle attempt 1 = %v, %v; want false, nil", settled, err)
	}
	if got, _ := store.JobRunGetByID(ctx, run.ID); got.DeadLetterCount != 0 {
		t.Fatalf("dead letters = %d, want 0", got.DeadLetterCount)
	}
}
