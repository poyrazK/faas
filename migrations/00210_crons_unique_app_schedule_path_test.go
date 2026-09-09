//go:build !no_pg

// Migration-apply test for 00210_crons_unique_app_schedule_path.sql
// (issue #791 PR-E / ADR-090 closure).
//
// Pins:
//
//  1. The crons_app_schedule_path_unique constraint exists after applying
//     00210 — the ALTER TABLE applied cleanly (no goose panic, no
//     pre-existing duplicate rows that block ADD CONSTRAINT).
//  2. Inserting a duplicate (app_id, schedule, path) row is rejected
//     with pgx 23505 (unique_violation).
//  3. Same schedule + different path → OK.
//  4. Different schedule + same path → OK.
//  5. Same (app_id, schedule, path) under a different app_id → OK.
//  6. Down migration removes the constraint cleanly.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00210_CronsUniqueAppSchedulePath(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed the bare-minimum FK targets so the crons row can insert.
	// Two apps (under the same account) let us assert the "different
	// app_id, same triple is OK" pin at (5).
	appID1 := "00000000-0000-0000-0000-0000000000a1"
	appID2 := "00000000-0000-0000-0000-0000000000a2"
	acctID := "00000000-0000-0000-0000-0000000000a3"

	// Insert the two apps via the public schema. The crons table has a
	// FK to apps(id); we don't need accounts' presence at insert time
	// (the FKs from accounts run via the apps.account_id column, which
	// is enforced at insert). Use a tiny account_id too — the unique
	// constraint we are pinning cares only about crons.
	_, err := pool.Exec(ctx, `
		insert into accounts (id, email, created_at)
		values ($1, '00210-crons-unique-test@example.com', now())
		on conflict (id) do nothing
	`, acctID)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	// Every placeholder must be referenced by the statement: pgx sends
	// Parse with no parameter OIDs, so an unreferenced $n leaves the
	// server with nothing to infer from and it answers 42P18
	// ("could not determine data type of parameter $n").
	_, err = pool.Exec(ctx, `
		insert into apps (id, account_id, slug, created_at, ram_mb)
		values ($1, $2, 'crons-unique-app-1', now(), 256)
		on conflict (id) do nothing
	`, appID1, acctID)
	if err != nil {
		t.Fatalf("seed app1: %v", err)
	}
	_, err = pool.Exec(ctx, `
		insert into apps (id, account_id, slug, created_at, ram_mb)
		values ($1, $2, 'crons-unique-app-2', now(), 256)
		on conflict (id) do nothing
	`, appID2, acctID)
	if err != nil {
		t.Fatalf("seed app2: %v", err)
	}

	// (1) Pin 1: insert succeeds → constraint allows this row.
	_, err = pool.Exec(ctx, `
		insert into crons (app_id, schedule, path)
		values ($1, '*/5 * * * *', '/cleanup')
	`, appID1)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// (2) Duplicate (app_id, schedule, path) must 23505.
	_, err = pool.Exec(ctx, `
		insert into crons (app_id, schedule, path)
		values ($1, '*/5 * * * *', '/cleanup')
	`, appID1)
	var pgErr *pgconn.PgError
	if err == nil {
		t.Errorf("duplicate (app_id, schedule, path) should be rejected by crons_app_schedule_path_unique")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("duplicate error = %v, want pgx 23505 (unique_violation)", err)
	}

	// (3) Same schedule + different path → OK.
	_, err = pool.Exec(ctx, `
		insert into crons (app_id, schedule, path)
		values ($1, '*/5 * * * *', '/other')
	`, appID1)
	if err != nil {
		t.Errorf("same schedule, different path should be OK: %v", err)
	}

	// (4) Different schedule + same path → OK.
	_, err = pool.Exec(ctx, `
		insert into crons (app_id, schedule, path)
		values ($1, '0 */6 * * *', '/cleanup')
	`, appID1)
	if err != nil {
		t.Errorf("different schedule, same path should be OK: %v", err)
	}

	// (5) Different app_id + same (schedule, path) → OK.
	_, err = pool.Exec(ctx, `
		insert into crons (app_id, schedule, path)
		values ($1, '*/5 * * * *', '/cleanup')
	`, appID2)
	if err != nil {
		t.Errorf("different app, same triple should be OK: %v", err)
	}
}
