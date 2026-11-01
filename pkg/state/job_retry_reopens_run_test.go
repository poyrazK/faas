package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

type jobRetryRunStore interface {
	JobTaskMarkTerminal(context.Context, string, int, string, int, string, string, time.Time) error
	JobRunRecompute(context.Context, string) (state.JobRun, error)
	JobTaskRetry(context.Context, string, int, time.Time) error
	JobRunGetByID(context.Context, string) (state.JobRun, error)
}

func TestMem_JobTaskRetryReopensSettledRun(t *testing.T) {
	ms := state.NewMemStore()
	_, run, tasks := newJobAndRun(t, ms, "acct-"+uuid.NewString(), "retry-reopens")
	jobTaskRetryReopensSettledRun(t, ms, run, tasks)
}

func TestPg_JobTaskRetryReopensSettledRun(t *testing.T) {
	s, _, ctx := pgJobsStoreWithPool(t)
	_, run, tasks := pgJobsSeed(t, s, ctx, "retry-reopens-"+uuid.NewString()[:8])
	jobTaskRetryReopensSettledRun(t, s, run, tasks)
}

// PulseWatch F7: a partition failed with a retry left, and its sibling
// finished while the scheduler was still tearing down the failed VM. The
// sibling's recompute settled the run as failed; the retry re-queued only the
// task, so `job wait` exited 1 before attempt 2 ran and succeeded.
func jobTaskRetryReopensSettledRun(t *testing.T, s jobRetryRunStore, run state.JobRun, tasks []state.JobTask) {
	t.Helper()
	ctx := context.Background()
	if len(tasks) < 2 {
		t.Fatalf("seed fanned %d tasks, want at least 2", len(tasks))
	}
	now := time.Now()
	for _, task := range tasks[1:] {
		if err := s.JobTaskMarkTerminal(ctx, run.ID, task.TaskIndex, "succeeded", 0, "", "", now); err != nil {
			t.Fatalf("finish sibling task %d: %v", task.TaskIndex, err)
		}
	}
	if err := s.JobTaskMarkTerminal(ctx, run.ID, tasks[0].TaskIndex, "failed", 75, "user_error", "transient", now); err != nil {
		t.Fatalf("fail task 0: %v", err)
	}
	settled, err := s.JobRunRecompute(ctx, run.ID)
	if err != nil {
		t.Fatalf("sibling recompute: %v", err)
	}
	if settled.AggregateStatus != "failed" || settled.FinishedAt == nil {
		t.Fatalf("settled run = %s finished %v, want failed with a finish time", settled.AggregateStatus, settled.FinishedAt)
	}
	if err := s.JobTaskRetry(ctx, run.ID, tasks[0].TaskIndex, now.Add(5*time.Second)); err != nil {
		t.Fatalf("JobTaskRetry: %v", err)
	}
	reopened, err := s.JobRunGetByID(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.AggregateStatus != "running" || reopened.FinishedAt != nil {
		t.Fatalf("run after retry = %s finished %v, want running and unfinished", reopened.AggregateStatus, reopened.FinishedAt)
	}
	// A later recompute while the retry waits keeps the run open (in Postgres
	// it used to violate job_runs_terminal_pair_chk).
	again, err := s.JobRunRecompute(ctx, run.ID)
	if err != nil {
		t.Fatalf("recompute while the retry waits: %v", err)
	}
	if again.AggregateStatus != "running" || again.FinishedAt != nil {
		t.Fatalf("recomputed run = %s finished %v, want running and unfinished", again.AggregateStatus, again.FinishedAt)
	}
}
