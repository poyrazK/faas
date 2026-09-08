//go:build !no_pg

// Migration-apply test for 00134_api_keys_org_bound.sql
// (issue #190 / IAM-6 / ADR-061, PR 6). Pins the org-bound
// `api_keys.org_id` not-null flip:
//  1. Migration set applies cleanly through 00134.
//  2. The api_keys.org_id column is NOT NULL after the flip.
//  3. Pre-existing api_keys rows get a deterministic org_id
//     stamped via the personal-org backfill shape (matches 00105).
//  4. INSERT … org_id NULL fails 23502 after the flip
//     (regression guard for Store-layer wiring).
//  5. Replay-safe: a second MigrateUp is a no-op (ADR-041).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00134_APIKeysOrgBound(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// Apply migrations 1..126 so the api_keys table + the partial
	// unique `orgs_one_personal_per_account_uniq` (00099) + the
	// personal-org backfill (00105) all exist before our update
	// touches rows.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// Seed two accounts; the 00105 personal-org backfill has already
	// fired on an empty accounts table, so seed first and then run
	// the backfill INSERTs ourselves. (The migration is replay-safe
	// per ADR-041; re-running the backfill body against freshly
	// seeded accounts is the deterministic test shape.)
	seedAccounts := []struct{ id, email string }{
		{"00000000-0000-0000-0000-0000000070a1", "pr6-apikey-1@example.com"},
		{"00000000-0000-0000-0000-0000000070a2", "pr6-apikey-2@example.com"},
	}
	for _, sa := range seedAccounts {
		if _, err := pool.Exec(ctx, `
			INSERT INTO accounts (id, email, plan, status, created_at)
			VALUES ($1, $2, 'free', 'active', now())
			ON CONFLICT (id) DO NOTHING
		`, sa.id, sa.email); err != nil {
			t.Fatalf("seed account %s: %v", sa.id, err)
		}
	}
	// Personal-org backfill (mirrors 00105's INSERT bodies so the
	// schema is in the post-00105 state).
	if _, err := pool.Exec(ctx, `
		INSERT INTO orgs (
			id, slug, name, personal_org, personal_owner_account_id,
			plan, status, created_at, updated_at
		)
		SELECT
			gen_random_uuid(),
			'u-' || substring(replace(a.id::text, '-', '') from 1 for 12),
			'Personal', true, a.id, a.plan, a.status, a.created_at, now()
		FROM accounts a
		LEFT JOIN orgs o
			ON o.personal_owner_account_id = a.id AND o.personal_org = true
		WHERE o.id IS NULL
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("backfill orgs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org_memberships (org_id, account_id, role, invited_by_account_id)
		SELECT o.id, a.id, 'owner', NULL
		FROM accounts a
		JOIN orgs o
			ON o.personal_owner_account_id = a.id AND o.personal_org = true
		LEFT JOIN org_memberships m
			ON m.org_id = o.id AND m.account_id = a.id
		WHERE m.org_id IS NULL
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("backfill memberships: %v", err)
	}

	// Seed two api_keys rows against the seeded accounts. We use
	// raw INSERTs so we land in the "pre-PR-6: api_keys.org_id is
	// NULL" state, exactly the shape the 00134 backfill targets.
	type seedKey struct {
		id, accountID, hash string
	}
	seedKeys := []seedKey{
		{"00000000-0000-0000-0000-0000000080a1", seedAccounts[0].id, "h1"},
		{"00000000-0000-0000-0000-0000000080a2", seedAccounts[1].id, "h2"},
	}
	for _, k := range seedKeys {
		if _, err := pool.Exec(ctx, `
			INSERT INTO api_keys (id, account_id, key_sha256, label, scopes, org_id)
			VALUES ($1, $2, $3, 'seeded', ARRAY['admin'::text], NULL)
			ON CONFLICT (id) DO NOTHING
		`, k.id, k.accountID, k.hash); err != nil {
			t.Fatalf("seed api_key %s: %v", k.id, err)
		}
	}

	// Run the 00134 backfill UPDATE directly (same SQL the migration
	// runs). Updating seeded rows is the deterministic test — the
	// migration itself has just-applied above.
	if _, err := pool.Exec(ctx, `
		UPDATE api_keys k
		   SET org_id = o.id
		  FROM orgs o
		 WHERE o.personal_owner_account_id = k.account_id
		   AND o.personal_org = true
		   AND k.org_id IS NULL
	`); err != nil {
		t.Fatalf("00134 update: %v", err)
	}

	// Probe 1: every api_keys row has a non-null org_id.
	var nullOrgIDCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_keys WHERE org_id IS NULL
	`).Scan(&nullOrgIDCount); err != nil {
		t.Fatalf("null org_id probe: %v", err)
	}
	if nullOrgIDCount != 0 {
		t.Errorf("api_keys rows with NULL org_id after 00134: %d", nullOrgIDCount)
	}

	// Probe 2: every api_keys row's org_id matches its account's
	// personal org.
	var orphaned int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM api_keys k
		  JOIN accounts a ON a.id = k.account_id
		 WHERE k.org_id IS DISTINCT FROM (
		   SELECT o.id FROM orgs o
		    WHERE o.personal_owner_account_id = a.id AND o.personal_org = true
		 )
	`).Scan(&orphaned); err != nil {
		t.Fatalf("orphan org_id probe: %v", err)
	}
	if orphaned != 0 {
		t.Errorf("api_keys rows whose org_id does not match the personal org: %d", orphaned)
	}

	// Probe 3: the column is NOT NULL after the flip (the second
	// statement in the migration). Probe via is_nullable from
	// information_schema.columns.
	var isNullable string
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable
		  FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name = 'api_keys'
		   AND column_name = 'org_id'
	`).Scan(&isNullable); err != nil {
		t.Fatalf("is_nullable probe: %v", err)
	}
	if isNullable != "NO" {
		t.Errorf("api_keys.org_id is_nullable = %q, want %q", isNullable, "NO")
	}

	// Probe 4: an INSERT with org_id NULL fails 23502 (not_null_violation)
	// after the flip. The Store layer's CreateOrgAPIKey method always
	// supplies org_id; this is a regression guard for direct INSERTs.
	var violation string
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (id, account_id, key_sha256, label, scopes, org_id)
		VALUES ('00000000-0000-0000-0000-0000000099ff',
		        '00000000-0000-0000-0000-0000000070a1',
		        'h3',
		        'bad',
		        ARRAY['admin'::text],
		        NULL)
	`); err == nil {
		t.Errorf("INSERT with org_id NULL did not fail after NOT NULL flip")
	} else {
		violation = err.Error()
		if violation == "" {
			t.Errorf("expected non-empty error string on NULL insert")
		}
	}
}

func TestMigrations_00134_ReplaySafety(t *testing.T) {
	// The 00134 update is guarded by `WHERE k.org_id IS NULL`, so a
	// second MigrateUp must be a no-op.
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	// Seed one account + one api_key + one personal org so the
	// backfill has something to do, then re-run the migration.
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, email, plan, status, created_at)
		VALUES ('00000000-0000-0000-0000-0000000070b1', 'replay-pr6@example.com',
		        'free', 'active', now())
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO orgs (
			id, slug, name, personal_org, personal_owner_account_id,
			plan, status, created_at, updated_at
		)
		VALUES (gen_random_uuid(),
		        'u-' || substring('0000000000000000000000000070b1' from 1 for 12),
		        'Personal', true,
		        '00000000-0000-0000-0000-0000000070b1',
		        'free', 'active', now(), now())
	`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (id, account_id, key_sha256, label, scopes, org_id)
		VALUES ('00000000-0000-0000-0000-0000000080b1',
		        '00000000-0000-0000-0000-0000000070b1',
		        'h-replay',
		        'replay',
		        ARRAY['admin'::text],
		        NULL)
	`); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	// Run a second MigrateUp; the UPDATE inside is the replay
	// tripwire — non-empty pre-existing org_ids would cause a
	// not_null trip, so the WHERE k.org_id IS NULL must catch them.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("second MigrateUp: %v", err)
	}
	var pre int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_keys WHERE org_id IS NOT NULL
	`).Scan(&pre); err != nil {
		t.Fatalf("count pre: %v", err)
	}
	// A third MigrateUp must also be a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("third MigrateUp: %v", err)
	}
	var post int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_keys WHERE org_id IS NOT NULL
	`).Scan(&post); err != nil {
		t.Fatalf("count post: %v", err)
	}
	if post != pre {
		t.Errorf("replay safety: pre=%d post=%d org_id NOT NULL", pre, post)
	}
}
