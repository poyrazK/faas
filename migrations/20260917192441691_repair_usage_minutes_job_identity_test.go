//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const repairUsageMinutesJobIdentityVersion int64 = 20260917192441691

func TestMigrations_RepairUsageMinutesJobIdentity(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}

	// Recreate the production drift: migration 00257 remains recorded while
	// the columns and constraints it introduced are absent.
	if _, err := pool.Exec(ctx, `
		drop index if exists usage_minutes_job_idx;
		alter table usage_minutes
			drop constraint if exists usage_minutes_app_or_job_chk,
			drop constraint if exists usage_minutes_meter_kind_check,
			drop column if exists job_id,
			drop column if exists meter_kind,
			alter column app_id set not null;
		delete from goose_db_version
		 where version_id = $1`, repairUsageMinutesJobIdentityVersion); err != nil {
		t.Fatalf("recreate production schema drift: %v", err)
	}
	assertUsageMinutesJobIdentity(t, ctx, pool, false)

	var legacyApplied int
	if err := pool.QueryRow(ctx, `
		select count(*) from goose_db_version
		 where version_id = 257 and is_applied`).Scan(&legacyApplied); err != nil {
		t.Fatalf("check legacy migration ledger row: %v", err)
	}
	if legacyApplied != 1 {
		t.Fatalf("legacy migration 257 ledger rows = %d, want 1", legacyApplied)
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply repair migration: %v", err)
	}
	assertUsageMinutesJobIdentity(t, ctx, pool, true)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay migrations: %v", err)
	}
	assertUsageMinutesJobIdentity(t, ctx, pool, true)
}

func assertUsageMinutesJobIdentity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want bool) {
	t.Helper()
	var appIDNullable, meterKind, jobID, meterKindCheck, identityCheck, jobIndex bool
	if err := pool.QueryRow(ctx, `
		select
			coalesce((
				select is_nullable = 'YES'
				  from information_schema.columns
				 where table_schema = current_schema()
				   and table_name = 'usage_minutes'
				   and column_name = 'app_id'
			), false),
			exists (
				select 1 from information_schema.columns
				 where table_schema = current_schema()
				   and table_name = 'usage_minutes'
				   and column_name = 'meter_kind'
			),
			exists (
				select 1 from information_schema.columns
				 where table_schema = current_schema()
				   and table_name = 'usage_minutes'
				   and column_name = 'job_id'
			),
			exists (
				select 1 from pg_constraint c
				 join pg_class t on t.oid = c.conrelid
				 where t.relnamespace = current_schema()::regnamespace
				   and t.relname = 'usage_minutes'
				   and c.conname = 'usage_minutes_meter_kind_check'
			),
			exists (
				select 1 from pg_constraint c
				 join pg_class t on t.oid = c.conrelid
				 where t.relnamespace = current_schema()::regnamespace
				   and t.relname = 'usage_minutes'
				   and c.conname = 'usage_minutes_app_or_job_chk'
			),
			to_regclass(current_schema() || '.usage_minutes_job_idx') is not null
	`).Scan(&appIDNullable, &meterKind, &jobID, &meterKindCheck, &identityCheck, &jobIndex); err != nil {
		t.Fatalf("inspect usage_minutes job identity: %v", err)
	}
	if appIDNullable != want || meterKind != want || jobID != want || meterKindCheck != want || identityCheck != want || jobIndex != want {
		t.Fatalf("usage_minutes job identity nullable=%t meter_kind=%t job_id=%t meter_check=%t identity_check=%t index=%t; want all %t",
			appIDNullable, meterKind, jobID, meterKindCheck, identityCheck, jobIndex, want)
	}
}
