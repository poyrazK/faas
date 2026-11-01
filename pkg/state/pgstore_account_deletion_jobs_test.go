package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// An account that ever ran a job could not be erased: job-task instances
// have no app_id, so DeleteAccount never deleted them, and the final
// `delete from accounts` cascaded to jobs and hit instances.job_id ON DELETE
// RESTRICT. The whole transaction rolled back, the grace sweep retried
// forever and none of the account's data was deleted (DPA erasure).
func TestPg_DeleteAccount_ErasesAnAccountThatRanJobs(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	job, run, tasks := pgJobsSeed(t, s, ctx, "erasure")
	nodeID := resolveDefaultLocal(t, ctx, s)
	instanceID := uuid.NewString()
	if _, err := s.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, tasks[0].TaskIndex,
		"cold_booting", 128, nodeID, instanceID, uuid.NewString(), time.Now().Add(5*time.Minute), nodeID); err != nil {
		t.Fatalf("claim job task: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, job.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, job.AccountID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if _, err := s.AccountByID(ctx, job.AccountID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account after erasure: %v, want ErrNotFound", err)
	}
	for _, q := range []string{
		`select count(*) from instances where job_id = $1`,
		`select count(*) from job_tasks where run_id in (select id from job_runs where job_id = $1)`,
		`select count(*) from jobs where id = $1`,
	} {
		var left int
		if err := pool.QueryRow(ctx, q, job.ID).Scan(&left); err != nil || left != 0 {
			t.Fatalf("%s = %d (%v), want 0", q, left, err)
		}
	}
}

// debug_regression_observations has no FK to apps. The app purge deleted an
// app's rows but account erasure did not, so a deleted account's per-route
// latency and error observations outlived it.
func TestPg_DeleteAccount_ErasesDebugRegressionObservations(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "regression-erasure@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "regression-erasure", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	factor, zero := pgtype.Numeric{}, pgtype.Numeric{}
	if err := factor.Scan("1.50"); err != nil {
		t.Fatal(err)
	}
	if err := zero.Scan("0"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRegressionObservation(ctx, sqlc.UpsertRegressionObservationParams{
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Route:        "GET /orders", P95Ms: 300, P95BaseMs: 200, AffectedCount: 4,
		RegressionFactor: factor, Kind: "latency", ErrorRatePct: zero, ErrorRateBasePct: zero,
	}); err != nil {
		t.Fatalf("seed regression observation: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `select count(*) from debug_regression_observations where app_id = $1`, app.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("regression observations left = %d (%v), want 0", left, err)
	}
}
