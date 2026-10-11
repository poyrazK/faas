//go:build !no_pg

package migrations_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestJobRunEmptyCommandSnapshotMigration(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@empty-command.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(account_id,kind,name,image_ref,ram_mb,task_timeout_s,
		max_parallelism,retry_max,status,command,cron_schedule,cron_timezone)
		VALUES($1,'recurring','git-empty-command','registry.example/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
		512,300,1,0,'active','{}','0 * * * *','UTC') RETURNING id`, account.ID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := pool.QueryRow(ctx, `INSERT INTO job_runs(job_id,account_id,trigger_kind,tasks,parallelism,command)
		VALUES($1,$2,'scheduled',1,1,ARRAY[]::text[]) RETURNING id`, jobID, account.ID).Scan(&runID); err != nil {
		t.Fatalf("insert image-default scheduled command snapshot: %v", err)
	}
	var cardinality int
	if err := pool.QueryRow(ctx, `SELECT cardinality(command) FROM job_runs WHERE id=$1`, runID).Scan(&cardinality); err != nil || cardinality != 0 {
		t.Fatalf("empty command snapshot cardinality=%d err=%v", cardinality, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO job_runs(job_id,account_id,trigger_kind,tasks,parallelism,command)
		VALUES($1,$2,'scheduled',1,1,array_fill('x'::text,ARRAY[65]))`, jobID, account.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "job_runs_command_shape_check" {
		t.Fatalf("65-entry command snapshot error = %v, want bounded-command check", err)
	}
}
