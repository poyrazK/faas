//go:build !no_pg

package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestSourceScheduleMigrationPreservesLegacyClaims(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Temporary copies preserve the real CHECKs and indexes without unrelated
	// graph FKs or execution triggers. Exercise the exact migration's constraint
	// upgrade against its historical Job-claim shape on this connection only.
	if _, err := tx.Exec(ctx, `
CREATE TEMP TABLE environment_workload_qualification_requests
    (LIKE environment_workload_qualification_requests INCLUDING ALL);
CREATE TEMP TABLE environment_qualification_job_smoke_receipts
    (LIKE environment_qualification_job_smoke_receipts INCLUDING ALL);
ALTER TABLE environment_workload_qualification_requests DROP CONSTRAINT environment_workload_qualification_reserved_instance_shape;
ALTER TABLE environment_workload_qualification_requests ADD CONSTRAINT environment_workload_qualification_reserved_instance_shape
    CHECK ((phase='queued' AND reserved_instance_id IS NULL) OR (phase='claimed' AND
        ((execution_mode='job' AND reserved_instance_id IS NULL) OR (execution_mode<>'job' AND reserved_instance_id IS NOT NULL))));
`); err != nil {
		t.Fatal(err)
	}
	const legacyClaim = `INSERT INTO environment_workload_qualification_requests
    (graph_id,deployment_id,app_id,resource,artifact,frozen_inputs,execution_mode,phase,worker_id,lease_token,lease_until,attempt)
    VALUES(gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),'workload/report','{}','{}','job','claimed','old-worker','old-token',now()+interval '1 minute',1)`
	if _, err := tx.Exec(ctx, legacyClaim); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261016110000001_environment_source_scheduled_jobs.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The constraint upgrade ends where the replay-guarded function blocks begin;
	// cutting inside a dollar-quoted DO body would leave unterminated SQL.
	upgrade, _, ok := strings.Cut(string(raw), "-- +goose StatementBegin\nDO $preserve_signed_jobs$")
	if !ok {
		t.Fatal("missing constraint upgrade boundary")
	}
	if _, err := tx.Exec(ctx, upgrade); err != nil {
		t.Fatalf("upgrade discarded or rejected legacy claims: %v", err)
	}
	var retained int
	var validated bool
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM environment_workload_qualification_requests
        WHERE phase='claimed' AND execution_mode='job' AND reserved_instance_id IS NULL`).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("legacy claim not retained: %d %v", retained, err)
	}
	validationState := `SELECT convalidated FROM pg_constraint
        WHERE conrelid='pg_temp.environment_workload_qualification_requests'::regclass
          AND conname='environment_workload_qualification_reserved_instance_shape'`
	if err := tx.QueryRow(ctx, validationState).Scan(&validated); err != nil || validated {
		t.Fatalf("legacy exception incorrectly validated: %v %v", validated, err)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT new_claim"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, legacyClaim)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "environment_workload_qualification_reserved_instance_shape" {
		t.Fatalf("new unreserved claim accepted: %v", err)
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT new_claim"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "UPDATE environment_workload_qualification_requests SET reserved_instance_id=gen_random_uuid()"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, upgrade); err != nil {
		t.Fatalf("replay after legacy claims acquired reserved VMs: %v", err)
	}
	if err := tx.QueryRow(ctx, validationState).Scan(&validated); err != nil || !validated {
		t.Fatalf("current claim shape not validated: %v %v", validated, err)
	}
}
