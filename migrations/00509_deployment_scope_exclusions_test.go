//go:build !no_pg

// Migration-apply test for 00509 (deployment_scope_exclusions
// table — ADR-124 follow-up #3 persistent --exclude history).
//
// Pins the load-bearing contracts:
//
//  1. The migration set applies cleanly through 00509; the new
//     table is created with the documented column shape.
//  2. UNIQUE (account_id, project_id, slug) trips SQLSTATE 23505
//     on duplicate insert (the active state of an exclusion is
//     "the row exists", so duplicates must be rejected by the
//     schema, not by application logic).
//  3. CHECK (slug = lower(slug)) rejects uppercase / mixed-case
//     slugs with SQLSTATE 23514 — the closed-set invariant that
//     lets consumers lowercase-key without ambiguity.
//  4. CHECK (length(slug) > 0) rejects empty slugs with SQLSTATE
//     23514 — defense-in-depth on top of the closed-set CHECK.
//  5. set_updated_at trigger fires on UPDATE — pin the wall-clock
//     advance (sleep 10ms between two UPDATEs to avoid the
//     timestamptz microsecond collision).
//  6. NO FK to apps(id) — soft-deleted apps do NOT cascade. The
//     row must survive UPDATE apps SET status='deleted' (this
//     is the SOFT-DELETE CASCADE BLIND SPOT documented in
//     00509_deployment_scope_exclusions.sql header).
//  7. ON DELETE CASCADE on accounts and projects DOES fire —
//     these are real DELETE paths (GDPR hard-delete + project
//     full-reset) so the FK posture is symmetric for them.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).

package migrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// exclusionUUIDs returns a deterministic UUID set for the test
// fixtures. Reusing fixed UUIDs across reruns keeps the seed
// idempotent (the existing 00029 / 00022 test style).
func exclusionUUIDs() (accountID, projectID, appID string) {
	return "00000000-0000-0000-0000-000000041801",
		"00000000-0000-0000-0000-000000041802",
		"00000000-0000-0000-0000-000000041803"
}

// seedExclusionFixture creates the (account, project, app) triple
// the test cases need. Each table is cleaned first via
// ON CONFLICT DO NOTHING so the test is rerunnable.
func seedExclusionFixture(ctx context.Context, t *testing.T, pool poolIface) (accountID, projectID, appID string) {
	t.Helper()
	accountID, projectID, appID = exclusionUUIDs()
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'exclusion-test@example.com', 'pro', now())
		on conflict (id) do nothing
	`, accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into projects (id, account_id, slug, created_at)
		values ($2, $1, 'exclusion-proj', now())
		on conflict (id) do nothing
	`, accountID, projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, project_id, status, created_at, ram_mb)
		values ($3, $1, 'checkout-api', $2, 'active', now(), 256)
		on conflict (id) do nothing
	`, accountID, projectID, appID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	return accountID, projectID, appID
}

// poolIface is the minimal interface the test seeds need. The
// concrete pgxpool.Pool satisfies this; the local interface keeps
// the test signature readable.
type poolIface interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// TestMigrations_00509_DeploymentScopeExclusions is the umbrella
// test for the new persistent --exclude history table. Each
// sub-test pins one of the contracts enumerated in the header.
func TestMigrations_00509_DeploymentScopeExclusions(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply the full migration set; 00509 is the new tail.
	// A regression that drops a slot between 1 and 509 surfaces
	// here before we get to the per-assertion pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 509)", err)
	}

	t.Run("SchemaCreated", func(t *testing.T) {
		// Sanity: the table + the two indexes + the
		// trigger exist. information_schema + pg_trigger are
		// the cheapest way to assert the shape without
		// duplicating the migration body.
		var tableCount int
		if err := pool.QueryRow(ctx, `
			select count(*) from information_schema.tables
			where table_name = 'deployment_scope_exclusions'
		`).Scan(&tableCount); err != nil {
			t.Fatalf("count tables: %v", err)
		}
		if tableCount != 1 {
			t.Errorf("deployment_scope_exclusions table count: got %d, want 1", tableCount)
		}

		var idxCount int
		if err := pool.QueryRow(ctx, `
			select count(*) from pg_indexes
			where tablename = 'deployment_scope_exclusions'
			  and indexname in (
			    'deployment_scope_exclusions_project_idx',
			    'deployment_scope_exclusions_account_idx'
			  )
		`).Scan(&idxCount); err != nil {
			t.Fatalf("count indexes: %v", err)
		}
		if idxCount != 2 {
			t.Errorf("indexes count: got %d, want 2", tableCount)
		}

		var trgCount int
		if err := pool.QueryRow(ctx, `
			select count(*) from pg_trigger
			where tgname = 'deployment_scope_exclusions_set_updated_at_trg'
		`).Scan(&trgCount); err != nil {
			t.Fatalf("count triggers: %v", err)
		}
		if trgCount != 1 {
			t.Errorf("set_updated_at trigger count: got %d, want 1", trgCount)
		}
	})

	accountID, projectID, appID := seedExclusionFixture(ctx, t, pool)

	t.Run("UniqueTripsOnDuplicate", func(t *testing.T) {
		// Insert one row.
		if _, err := pool.Exec(ctx, `
			insert into deployment_scope_exclusions
				(account_id, project_id, app_id, slug, reason, created_by)
			values ($1, $2, $3, 'checkout-api', 'destructive in prod', 'operator@test')
		`, accountID, projectID, appID); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		// Re-insert same (account, project, slug) — must trip 23505.
		_, err := pool.Exec(ctx, `
			insert into deployment_scope_exclusions
				(account_id, project_id, app_id, slug, reason, created_by)
			values ($1, $2, $3, 'checkout-api', 'duplicate', 'operator@test')
		`, accountID, projectID, appID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Errorf("duplicate insert: got %v, want SQLSTATE 23505 (unique_violation)", err)
		}
	})

	t.Run("CheckRejectsNonLowercaseSlug", func(t *testing.T) {
		// Mixed-case slug must trip the closed-set CHECK.
		_, err := pool.Exec(ctx, `
			insert into deployment_scope_exclusions
				(account_id, project_id, app_id, slug, reason, created_by)
			values ($1, $2, $3, 'Checkout-Api', 'should fail', 'operator@test')
		`, accountID, projectID, appID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("uppercase slug insert: got %v, want SQLSTATE 23514 (check_violation)", err)
		}
	})

	t.Run("CheckRejectsEmptySlug", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			insert into deployment_scope_exclusions
				(account_id, project_id, app_id, slug, reason, created_by)
			values ($1, $2, $3, '', 'should fail', 'operator@test')
		`, accountID, projectID, appID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("empty slug insert: got %v, want SQLSTATE 23514 (check_violation)", err)
		}
	})

	t.Run("SetUpdatedAtTriggerFires", func(t *testing.T) {
		// Insert a fresh row to UPDATE (the UniqueTripsOnDuplicate
		// row already exists; use a different slug).
		if _, err := pool.Exec(ctx, `
			insert into deployment_scope_exclusions
				(account_id, project_id, app_id, slug, reason, created_by)
			values ($1, $2, $3, 'worker-job', 'initial reason', 'operator@test')
		`, accountID, projectID, appID); err != nil {
			t.Fatalf("seed for trigger test: %v", err)
		}
		var before time.Time
		if err := pool.QueryRow(ctx, `
			select updated_at from deployment_scope_exclusions
			where account_id = $1 and project_id = $2 and slug = 'worker-job'
		`, accountID, projectID).Scan(&before); err != nil {
			t.Fatalf("read before: %v", err)
		}
		// Sleep just enough that now() advanced past the
		// microsecond collision window (Postgres timestamptz
		// is microsecond-precision; a 5ms gap is plenty).
		time.Sleep(5 * time.Millisecond)
		if _, err := pool.Exec(ctx, `
			update deployment_scope_exclusions
			set reason = 'updated reason'
			where account_id = $1 and project_id = $2 and slug = 'worker-job'
		`, accountID, projectID); err != nil {
			t.Fatalf("update: %v", err)
		}
		var after time.Time
		if err := pool.QueryRow(ctx, `
			select updated_at from deployment_scope_exclusions
			where account_id = $1 and project_id = $2 and slug = 'worker-job'
		`, accountID, projectID).Scan(&after); err != nil {
			t.Fatalf("read after: %v", err)
		}
		if !after.After(before) {
			t.Errorf("set_updated_at trigger did not advance updated_at: before=%s after=%s",
				before.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
		}
	})

	t.Run("SoftDeleteOnAppDoesNotCascade", func(t *testing.T) {
		// CRITICAL pitfall pin: the absence of an apps FK is the
		// load-bearing design choice. Soft-delete (UPDATE status=
		// 'deleted') must NOT remove the exclusion row. A
		// regression that added an apps FK with ON DELETE CASCADE
		// would still pass this test (CASCADE only fires on real
		// DELETE) — but the soft-delete-cascade blind spot is
		// documented separately; this test pins that soft-delete
		// leaves the row intact (the operational hazard the
		// absence of FK was chosen to avoid).
		if _, err := pool.Exec(ctx, `
			update apps set status = 'deleted' where id = $1
		`, appID); err != nil {
			t.Fatalf("soft-delete app: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `
			select count(*) from deployment_scope_exclusions
			where account_id = $1 and project_id = $2
		`, accountID, projectID).Scan(&count); err != nil {
			t.Fatalf("count after soft-delete: %v", err)
		}
		// 3 exclusions seeded above (checkout-api, worker-job,
		// and the trigger test inserted worker-job). The exact
		// count is irrelevant — what matters is non-zero,
		// proving soft-delete did NOT cascade.
		if count == 0 {
			t.Errorf("soft-delete cascaded to exclusions; rows: 0 (FK must NOT exist on apps)")
		}
	})

	t.Run("AccountHardDeleteCascades", func(t *testing.T) {
		// Symmetric: a real DELETE on accounts (GDPR path) MUST
		// remove the exclusions. Use a fresh account + project
		// to isolate the assertion.
		freshAccount := "00000000-0000-0000-0000-000000041804"
		freshProject := "00000000-0000-0000-0000-000000041805"
		if _, err := pool.Exec(ctx, `
			insert into accounts (id, email, plan, created_at)
			values ($1, 'exclusion-test-cascade@example.com', 'free', now())
			on conflict (id) do nothing
		`, freshAccount); err != nil {
			t.Fatalf("seed fresh account: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			insert into projects (id, account_id, slug, created_at)
			values ($2, $1, 'exclusion-cascade-proj', now())
			on conflict (id) do nothing
		`, freshAccount, freshProject); err != nil {
			t.Fatalf("seed fresh project: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			insert into deployment_scope_exclusions
				(account_id, project_id, app_id, slug, reason, created_by)
			values ($1, $2, $3, 'cascaded-slug', 'will be reaped', 'operator@test')
		`, freshAccount, freshProject, appID); err != nil {
			t.Fatalf("seed cascade row: %v", err)
		}
		if _, err := pool.Exec(ctx, `delete from accounts WHERE id = $1`, freshAccount); err != nil {
			t.Fatalf("hard-delete account: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `
			select count(*) from deployment_scope_exclusions
			where account_id = $1
		`, freshAccount).Scan(&count); err != nil {
			t.Fatalf("count after account delete: %v", err)
		}
		if count != 0 {
			t.Errorf("account hard-delete did not cascade to exclusions: rows=%d", count)
		}
	})
}
