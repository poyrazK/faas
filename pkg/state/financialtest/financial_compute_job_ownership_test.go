package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1747 — separate job/run account FKs do not prove shared ownership.
// Legacy writers cannot evade the job owner's cap using another account's run.
func TestFinancialComputeJobForeignRunOwner(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	f := newAccountComputeFixture(t, store, 0, 1000)
	job, _ := computeJob(t, f, "batch")
	other := financialAccount(t, store)
	run, instance, token := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"job_runs"}, []string{"id", "job_id", "account_id", "trigger_kind", "tasks", "parallelism"}, pgx.CopyFromRows([][]any{{run, job.ID, other.ID, "manual", int32(1), int32(1)}}))
	if err != nil {
		t.Fatalf("independent foreign keys fixture: %v", err)
	}
	owner := f.owners[0]
	if _, err := store.CreateJobInstance(ctx, instance, job.ID, run, 0, string(state.StateColdBooting), 128, owner.NodeID, instance); err != nil {
		t.Fatal(err)
	}
	_, err = pool.CopyFrom(ctx, pgx.Identifier{"job_tasks"}, []string{"run_id", "task_index", "status", "instance_id", "lease_token", "lease_expires_at"}, pgx.CopyFromRows([][]any{{run, int32(0), "claimed", instance, token, time.Now().Add(time.Hour)}}))
	var check *pgconn.PgError
	if !errors.As(err, &check) || check.Code != "23514" || check.ConstraintName != "financial_compute_job_claim_identity" {
		t.Fatalf("legacy claim crossed job ownership: %v", err)
	}
	request := financial.ComputeRequest{AccountID: other.ID, JobID: job.ID, JobRunID: run, JobTaskIndex: 0, InstanceID: instance, NodeID: owner.NodeID, Incarnation: owner.Incarnation, AttemptID: financial.ComputeJobAttemptID(run, 0, token), Fenced: true, BillableMB: 136}
	if _, err := store.AcquireFinancialCompute(ctx, request); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("foreign run funded: %v", err)
	}
	if b := f.balance(t); b.ReservedMillicents != 0 || b.CommittedMillicents != 0 || b.UnconfirmedLeases != 0 {
		t.Fatalf("foreign run changed job owner's budget: %+v", b)
	}
}
