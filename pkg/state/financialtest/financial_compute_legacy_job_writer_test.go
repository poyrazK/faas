package financialtest

import (
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Bypass current application admission using the driver's bulk-copy protocol.
// The database must also reject a definition from an older intent writer.
func TestFinancialComputeAccountLegacyJobWriter(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	f := newAccountComputeFixture(t, store, 0, 1000, false)
	job, err := store.JobCreate(ctx, f.account.ID, "before-cap", "batch", "example/image", []string{}, 128, 30, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, f.account.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobRunCancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, live, err := store.JobSoftDelete(ctx, job.ID); err != nil || live {
		t.Fatalf("retire prior job: %t %v", live, err)
	}
	for at := f.account.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	f.policy, err = store.CreateFinancialBudget(ctx, f.account.ID, uuid.NewString(), "test", f.policy.Spec)
	if err != nil {
		t.Fatal(err)
	}
	insert := func() error {
		_, err := pool.CopyFrom(ctx, pgx.Identifier{"jobs"}, []string{"account_id", "kind", "name", "image_ref", "ram_mb", "task_timeout_s", "max_parallelism", "retry_max", "env_overrides", "command"}, pgx.CopyFromRows([][]any{{f.account.ID, "batch", "legacy-job", "example/image", int32(128), int32(30), int32(1), int32(0), []byte("{}"), []string{}}}))
		return err
	}
	var check *pgconn.PgError
	if err := insert(); !errors.As(err, &check) || check.Code != "23514" || check.ConstraintName != "financial_compute_account_job_supported" {
		t.Fatalf("legacy job bypassed account cap: %v", err)
	}
	_, err = pool.CopyFrom(ctx, pgx.Identifier{"job_tasks"}, []string{"run_id", "task_index", "status"}, pgx.CopyFromRows([][]any{{run.ID, int32(1), "queued"}}))
	check = nil
	if !errors.As(err, &check) || check.Code != "23514" || check.ConstraintName != "financial_compute_account_job_supported" {
		t.Fatalf("legacy task bypassed account cap: %v", err)
	}
	if count, err := store.JobCountByAccount(ctx, f.account.ID); err != nil || count != 0 {
		t.Fatalf("rejected job persisted: %d %v", count, err)
	}
	if _, err := store.DeleteFinancialBudget(ctx, f.account.ID, f.policy.ID, f.policy.Revision, "test"); err != nil {
		t.Fatal(err)
	}
	if err := insert(); err != nil {
		t.Fatalf("disabled policy still blocked ordinary job definitions: %v", err)
	}
	if count, err := store.JobCountByAccount(ctx, f.account.ID); err != nil || count != 1 {
		t.Fatalf("ordinary definition: %d %v", count, err)
	}
}
