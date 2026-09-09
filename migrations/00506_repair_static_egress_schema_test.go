//go:build !no_pg

// Regression test for 00506_repair_static_egress_schema.sql.
//
// It models the production failure: goose has already recorded 00336 and
// 00337, while the schema objects owned by those historical slots are absent.
// The append-only repair must restore the objects when 00506 is still
// pending, and a second run must be a no-op.
//
// The scenario is built by migrating only as far as 00505 (goose UpTo), so
// 00336/00337 are recorded in the ledger while 00506 and everything after it
// are genuinely pending — exactly the deployed state 00506 was written for.
// It deliberately does NOT delete 00506's ledger row from a fully-migrated
// database: goose's strict findMissingMigrations refuses a legacy-numbered
// gap below the current version (the migration set now runs well past 00506
// into the ADR-142 timestamp range), so that shortcut no longer reproduces
// the production state.
package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// repairMigrationVersion is the slot owned by
// 00506_repair_static_egress_schema.sql; the scenario stops one slot short of
// it so the repair itself is the pending migration under test.
const repairMigrationVersion int64 = 506

func TestMigrations_00506RepairsStaticEgressSchemaDrift(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// Stop one slot short of the repair so 00506 is still pending, the way
	// the deployed database looked when the repair was written.
	migrateUpTo(t, ctx, pool, repairMigrationVersion-1)

	assertStaticEgressSchema(t, ctx, pool)

	// Keep the historical ledger entries in place. This is the important
	// detail: 00336 and 00337 are already applied, so goose will not replay
	// their now-correct source files, and the repair is the only migration
	// that can put the objects back.
	var applied int
	if err := pool.QueryRow(ctx, `
		select count(*)
		  from goose_db_version
		 where version_id in (336, 337)`).Scan(&applied); err != nil {
		t.Fatalf("check historical migration ledger rows: %v", err)
	}
	if applied != 2 {
		t.Fatalf("historical static-egress migration rows = %d, want 2", applied)
	}

	// The repair must not already be recorded, otherwise the assertions
	// below would pass without 00506 ever running.
	var repairApplied int
	if err := pool.QueryRow(ctx, `
		select count(*)
		  from goose_db_version
		 where version_id = $1`, repairMigrationVersion).Scan(&repairApplied); err != nil {
		t.Fatalf("check repair migration ledger row: %v", err)
	}
	if repairApplied != 0 {
		t.Fatalf("repair migration ledger rows = %d, want 0 (00506 must still be pending)", repairApplied)
	}

	// Recreate the deployed drift: the ledger says the feature is present,
	// but its schema objects are missing. The later migration owns no data in
	// this isolated test, so removing the objects is safe and deterministic.
	if _, err := pool.Exec(ctx, `
		drop index if exists apps_static_egress_ip_key;
		alter table apps drop constraint if exists apps_static_egress_ip_family_check;
		alter table apps drop column if exists static_egress_ip_set_at;
		alter table apps drop column if exists static_egress_ip;
		drop table if exists provisioned_static_egress_ips`); err != nil {
		t.Fatalf("create drifted schema: %v", err)
	}
	assertStaticEgressSchemaAbsent(t, ctx, pool)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("repair db.MigrateUp: %v", err)
	}
	assertStaticEgressSchema(t, ctx, pool)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v", err)
	}
	assertStaticEgressSchema(t, ctx, pool)
}

// migrateUpTo applies the embedded migration set through version (inclusive)
// against the pool's isolated schema. It mirrors what db.MigrateUp does with
// the goose shim, minus the advisory lock (each test owns its own schema) and
// with an upper bound so a prefix of the history can be staged.
func migrateUpTo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, version int64) {
	t.Helper()
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		t.Fatal("migrateUpTo: pool has no config")
	}
	sqlDB, err := sql.Open("pgx", stdlib.RegisterConnConfig(cfg.ConnConfig))
	if err != nil {
		t.Fatalf("migrateUpTo: open stdlib: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("migrateUpTo: set goose dialect: %v", err)
	}
	if err := goose.UpToContext(ctx, sqlDB, ".", version); err != nil {
		t.Fatalf("migrateUpTo(%d): %v", version, err)
	}

	var got int64
	if err := pool.QueryRow(ctx, `
		select coalesce(max(version_id), 0)
		  from goose_db_version
		 where is_applied`).Scan(&got); err != nil {
		t.Fatalf("migrateUpTo: read ledger version: %v", err)
	}
	if got != version {
		t.Fatalf("migrateUpTo: ledger at version %d, want %d (the staged prefix must stop exactly below the repair)", got, version)
	}
}

// assertStaticEgressSchemaAbsent is the tripwire for the drift setup: if the
// DROP block above ever stops removing the objects, the repair assertions
// would pass vacuously.
func assertStaticEgressSchemaAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var column, table bool
	if err := pool.QueryRow(ctx, `
		select
			exists (
				select 1
				  from information_schema.columns
				 where table_schema = current_schema()
				   and table_name = 'apps'
				   and column_name = 'static_egress_ip'
			),
			exists (
				select 1
				  from pg_class
				 where relnamespace = current_schema()::regnamespace
				   and relname = 'provisioned_static_egress_ips'
			)`).Scan(&column, &table); err != nil {
		t.Fatalf("check drifted schema: %v", err)
	}
	if column {
		t.Fatal("apps.static_egress_ip still present; the drift setup did not take effect")
	}
	if table {
		t.Fatal("provisioned_static_egress_ips still present; the drift setup did not take effect")
	}
}

func assertStaticEgressSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	for _, column := range []string{"static_egress_ip", "static_egress_ip_set_at"} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			select exists (
				select 1
				  from information_schema.columns
				 where table_schema = current_schema()
				   and table_name = 'apps'
				   and column_name = $1
			)`, column).Scan(&exists); err != nil {
			t.Fatalf("check apps.%s: %v", column, err)
		}
		if !exists {
			t.Errorf("apps.%s is missing", column)
		}
	}

	var appsIndex, appsCheck, tableExists, customerIPIndex bool
	if err := pool.QueryRow(ctx, `
		select
			exists (
				select 1 from pg_indexes
				 where schemaname = current_schema()
				   and tablename = 'apps'
				   and indexname = 'apps_static_egress_ip_key'
			),
			exists (
				select 1
				  from pg_constraint c
				  join pg_class t on t.oid = c.conrelid
				 where t.relnamespace = current_schema()::regnamespace
				   and t.relname = 'apps'
				   and c.conname = 'apps_static_egress_ip_family_check'
			),
			exists (
				select 1
				  from pg_class
				 where relnamespace = current_schema()::regnamespace
				   and relname = 'provisioned_static_egress_ips'
			),
			exists (
				select 1 from pg_indexes
				 where schemaname = current_schema()
				   and tablename = 'provisioned_static_egress_ips'
				   and indexname = 'provisioned_static_egress_ips_customer_ip_idx'
			)`).Scan(&appsIndex, &appsCheck, &tableExists, &customerIPIndex); err != nil {
		t.Fatalf("check static-egress indexes and constraints: %v", err)
	}
	if !appsIndex {
		t.Error("apps_static_egress_ip_key is missing")
	}
	if !appsCheck {
		t.Error("apps_static_egress_ip_family_check is missing")
	}
	if !tableExists {
		t.Error("provisioned_static_egress_ips is missing")
	}
	if !customerIPIndex {
		t.Error("provisioned_static_egress_ips_customer_ip_idx is missing")
	}
}
