//go:build !no_pg

// adr: 581 — conservative upgrade and guarded retirement of accounting intent.

package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedPostgresAccountingIntentMigrationUpgradeAndRollback(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261004153454875)
	account, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@accounting-intent.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, lifecycle := range []string{"provisioning", "failed", "deleting", "deleted", "ready"} {
		_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_databases
(id,account_id,name,region,postgres_major,service_class,availability,scale_to_zero,storage_limit_bytes,
restore_window_seconds,backend_id,backend_fingerprint,state,provider_resource_id,deleted_at,observed_generation)
VALUES($1,$2,$3,'us-east-1',17,'development','single_zone',true,4096,0,'primary-a',$4,$5,
CASE WHEN $5='ready' THEN 'legacy-provider' ELSE NULL END,CASE WHEN $5='deleted' THEN now() ELSE NULL END,
CASE WHEN $5='ready' THEN 1 ELSE 0 END)`,
			uuid.New(), account.ID, "legacy-"+lifecycle, strings.Repeat("a", 64), lifecycle)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_postgres_databases WHERE accounting_required`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("historical obligations: count=%d err=%v", count, err)
	}
	newID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_databases
(id,account_id,name,region,postgres_major,service_class,availability,scale_to_zero,storage_limit_bytes,
restore_window_seconds,backend_id,backend_fingerprint,state,accounting_required)
VALUES($1,$2,'unattempted','us-east-1',17,'development','single_zone',true,4096,0,'primary-a',$3,'provisioning',false)`, newID, account.ID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	var required bool
	if err := pool.QueryRow(ctx, `SELECT accounting_required FROM managed_postgres_databases WHERE id=$1`, newID).Scan(&required); err != nil || required {
		t.Fatalf("explicit unattempted reservation: required=%v err=%v", required, err)
	}

	legacyInsertID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_databases
(id,account_id,name,region,postgres_major,service_class,availability,scale_to_zero,storage_limit_bytes,
restore_window_seconds,backend_id,backend_fingerprint,state)
VALUES($1,$2,'legacy-writer','us-east-1',17,'development','single_zone',true,4096,0,'primary-a',$3,'provisioning')`, legacyInsertID, account.ID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT accounting_required FROM managed_postgres_databases WHERE id=$1`, legacyInsertID).Scan(&required); err != nil || !required {
		t.Fatalf("legacy insert lost obligation: required=%v err=%v", required, err)
	}
	raw, err := migrations.FS.ReadFile("20261004184850283_managed_postgres_accounting_intent.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT accounting_required FROM managed_postgres_databases WHERE id=$1`, newID).Scan(&required); err != nil || required {
		t.Fatalf("replay rewrote unattempted intent: required=%v err=%v", required, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET accounting_required=true WHERE id=$1`, newID); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE managed_postgres_databases SET accounting_required=false WHERE id=$1`, newID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "managed_postgres_accounting_intent_retained" {
		t.Fatalf("clearing obligation: %v", err)
	}
	_, err = pool.Exec(ctx, sections[1])
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "managed_postgres_accounting_downgrade_unresolved" {
		t.Fatalf("downgrade forgot unresolved accounting: %v", err)
	}
	// Model recovered identities; the previous schema still meters known IDs.
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET provider_resource_id='recovered-'||id::text WHERE provider_resource_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_postgres_databases WHERE accounting_required`).Scan(&count); err != nil || count != 7 {
		t.Fatalf("populated replay: count=%d err=%v", count, err)
	}
}
