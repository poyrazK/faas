package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An account that ever ran a job could not be erased: job-task instances
// have no app_id, so DeleteAccount never deleted them, and the final
// `delete from accounts` cascaded to jobs and hit instances.job_id ON DELETE
// RESTRICT. The whole transaction rolled back, the grace sweep retried
// forever and none of the account's data was deleted (DPA erasure).
func TestPg_DeleteAccount_ErasesAnAccountThatRanJobs(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "jobs-erasure@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := pool.QueryRow(ctx, `insert into jobs (account_id, kind, name, image_ref, ram_mb, task_timeout_s, max_parallelism, retry_max, command)
		values ($1, 'batch', 'nightly', 'registry.example/job@sha256:0000000000000000000000000000000000000000000000000000000000000000', 256, 60, 1, 0, '{}')
		returning id`, acct.ID).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into instances (kind, job_id, state, ram_mb, node_id) values ('job_task', $1, 'stopped', 256, $2)`,
		jobID, resolveDefaultLocal(t, ctx, s)); err != nil {
		t.Fatalf("seed job-task instance: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if _, err := s.AccountByID(ctx, acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account after erasure: %v, want ErrNotFound", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `select count(*) from instances where job_id = $1`, jobID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("job-task instances left = %d (%v), want 0", left, err)
	}
}
