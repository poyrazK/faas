// adr: 385 — a dead-lettered attempt is a permanent failure for fail-fast,
// and a refused automatic retry dead-letters its attempt.

package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// A rules-free failure on its last attempt records dead_letter
// (retry_budget_exhausted), not retry. Fail-fast must still stop the run:
// before, its SQL matched only fail_partition or retry past the budget, so
// every unstarted task of a default fail-fast run kept running.
func TestPg_Jobs_FailFastStopsAfterDeadLetteredAttempt(t *testing.T) {
	s, pool, ctx := pgJobsStoreWithPool(t)
	job, _, _ := pgJobsSeed(t, s, ctx, "fail-fast-dead-letter")
	zero := 0
	run, _, err := s.JobRunCreate(ctx, job.ID, job.AccountID, "manual", nil, &zero, nil, nil, 3,
		state.JobRunOptions{FailurePolicy: "fail_fast"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.JobTaskMarkTerminal(ctx, run.ID, 0, "failed", 1, "failed", "customer failure", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update job_tasks set work_decision = $3::jsonb where run_id = $1::uuid and task_index = $2`,
		run.ID, 0, `{"policy_version":1,"classification":"retryable","action":"dead_letter","reason":"retry_budget_exhausted"}`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.JobTaskClaimBatch(ctx, 10); err != nil {
		t.Fatal(err)
	} else {
		for _, task := range claimed {
			if task.RunID == run.ID {
				t.Fatalf("claimed task %d of a fail-fast run with a dead-lettered task", task.TaskIndex)
			}
		}
	}
	if _, err := s.JobRunRecompute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := s.JobTaskList(ctx, run.ID, 10, 0)
	if err != nil || len(listed) != 3 || listed[1].Status != "cancelled" || listed[2].Status != "cancelled" {
		t.Fatalf("fail-fast tasks = %+v, err = %v; want tasks 1 and 2 cancelled", listed, err)
	}
}

func TestPg_Jobs_SettleRefusedJobTaskRetry(t *testing.T) {
	s, pool, ctx := pgJobsStoreWithPool(t)
	_, run, fanned := pgJobsSeed(t, s, ctx, "refused-retry")
	task := fanned[0]
	if err := s.JobTaskMarkTerminal(ctx, run.ID, task.TaskIndex, "failed", 1, "failed", "customer failure", time.Now()); err != nil {
		t.Fatal(err)
	}
	retry := `{"policy_version":1,"classification":"retryable","action":"retry","reason":"legacy_failure"}`
	for _, table := range []string{"job_tasks", "job_task_attempts"} {
		if _, err := pool.Exec(ctx, `update `+table+` set work_decision = $3::jsonb where run_id = $1::uuid and task_index = $2`,
			run.ID, task.TaskIndex, retry); err != nil {
			t.Fatal(err)
		}
	}
	decision := workpolicy.Decision{PolicyVersion: workpolicy.Version, Classification: "retryable", Action: "dead_letter", Reason: "retry_refused"}

	// Another attempt number is a newer attempt: left alone.
	if settled, err := s.SettleRefusedJobTaskRetry(ctx, run.ID, task.TaskIndex, task.Attempt+1, decision); err != nil || settled {
		t.Fatalf("settle a stale attempt = %v, %v; want false, nil", settled, err)
	}
	settled, err := s.SettleRefusedJobTaskRetry(ctx, run.ID, task.TaskIndex, task.Attempt, decision)
	if err != nil || !settled {
		t.Fatalf("SettleRefusedJobTaskRetry = %v, %v; want true", settled, err)
	}
	got, err := s.JobTaskGet(ctx, run.ID, task.TaskIndex)
	if err != nil || got.WorkDecision == nil || got.WorkDecision.Action != "dead_letter" || got.WorkDecision.Reason != "retry_refused" {
		t.Fatalf("task decision = %+v, err %v; want dead_letter/retry_refused", got.WorkDecision, err)
	}
	var journal json.RawMessage
	if err := pool.QueryRow(ctx, `select work_decision from job_task_attempts where run_id = $1::uuid and task_index = $2 and attempt = $3`,
		run.ID, task.TaskIndex, task.Attempt).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	var recorded workpolicy.Decision
	if err := json.Unmarshal(journal, &recorded); err != nil || recorded.Action != "dead_letter" {
		t.Fatalf("journal decision = %s, err %v; want dead_letter", journal, err)
	}
	if again, err := s.SettleRefusedJobTaskRetry(ctx, run.ID, task.TaskIndex, task.Attempt, decision); err != nil || again {
		t.Fatalf("second settle = %v, %v; want false, nil (counted once)", again, err)
	}
	settledRun, err := s.JobRunRecompute(ctx, run.ID)
	if err != nil || settledRun.DeadLetterCount != 1 {
		t.Fatalf("run = %+v, err %v; want one dead letter", settledRun, err)
	}
}
