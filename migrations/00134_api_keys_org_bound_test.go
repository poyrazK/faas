//go:build !no_pg

// Migration-apply test for 00134_api_keys_org_bound.sql
// (issue #190 / IAM-6 / ADR-061, PR 6).
//
// Shape note (2026-09): this test used to seed api_keys rows with
// org_id = NULL to reproduce the pre-00134 state and then run the
// migration's backfill UPDATE by hand. That state is unreachable
// against the current migration set — 00134 itself flips the column
// to NOT NULL, so the seed trips 23502 before the backfill can be
// exercised. Re-creating it would mean dropping the NOT NULL, i.e.
// testing a schema production never has. The tests below pin the END
// STATE the migration established instead, which is what every reader
// and writer downstream actually depends on:
//
//  1. Migration set applies cleanly through 00134.
//  2. api_keys.org_id is NOT NULL (the flip), and an INSERT with
//     org_id NULL fails 23502 — the regression guard for direct
//     INSERTs that bypass the Store layer's CreateOrgAPIKey.
//  3. api_keys.org_id is FK'd to orgs(id) with ON DELETE RESTRICT,
//     and the RESTRICT actually bites: deleting an org that still
//     owns keys fails 23503. That is the "no implicit data loss"
//     property PR-8's GDPR org-purge leans on (see the migration
//     header) — CASCADE here would silently nuke keys.
//  4. A dangling org_id fails 23503 (the FK parent must exist).
//  5. Every key resolves to its account's personal org — the
//     invariant the backfill established (join via the partial
//     unique orgs_one_personal_per_account_uniq from 00099).
//  6. Replay-safety (ADR-041): further MigrateUps are no-ops, AND
//     re-running the migration's backfill UPDATE body verbatim
//     updates zero rows and leaves an already-stamped org_id alone.
//     That second half is what pins the `WHERE k.org_id IS NULL`
//     guard — a key deliberately bound to a SHARED org must not be
//     rewritten to its owner's personal org on replay.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// backfill00134Body is 00134's step-1 UPDATE, copied verbatim from
// migrations/00134_api_keys_org_bound.sql. Kept as a constant so the
// replay test below runs the real statement rather than a paraphrase.
const backfill00134Body = `
UPDATE api_keys k
   SET org_id = o.id
  FROM orgs o
 WHERE o.personal_owner_account_id = k.account_id
   AND o.personal_org = true
   AND k.org_id IS NULL
`

func TestMigrations_00134_APIKeysOrgBound(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply the full set. 00099 adds api_keys.org_id + the FK,
	// 00105 backfills personal orgs, 00134 stamps every key and flips
	// the column NOT NULL.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// Seed two accounts, each with the personal org 00105 would have
	// created for it (pgtest.Open hands us an empty schema, so the
	// migration's own backfill had no rows to act on).
	type acct struct{ accountID, orgID string }
	accts := make([]acct, 0, 2)
	for i := 0; i < 2; i++ {
		accountID := seedAccount(t, ctx, pool)
		accts = append(accts, acct{accountID, seedPersonalOrg(t, ctx, pool, accountID)})
	}

	// Seed one key per account, stamped the way the migration's join
	// stamps it (and the way the Store layer's CreateOrgAPIKey does):
	// org_id = the account's personal org, resolved through orgs.
	for i, a := range accts {
		if _, err := pool.Exec(ctx, `
			INSERT INTO api_keys (account_id, key_sha256, label, scopes, org_id)
			SELECT $1, $2, 'seeded', ARRAY['admin'::text], o.id
			  FROM orgs o
			 WHERE o.personal_owner_account_id = $1
			   AND o.personal_org = true
		`, a.accountID, []byte("00134-seed-key-"+a.accountID)); err != nil {
			t.Fatalf("seed api_key %d: %v", i, err)
		}
	}
	var seeded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys`).Scan(&seeded); err != nil {
		t.Fatalf("count seeded keys: %v", err)
	}
	if seeded != len(accts) {
		t.Fatalf("seeded api_keys = %d, want %d (the personal-org join found no parent)", seeded, len(accts))
	}

	// (2) The column is NOT NULL after the flip.
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

	// (2b) …and an explicit NULL is rejected with 23502.
	_, err := pool.Exec(ctx, `
		INSERT INTO api_keys (account_id, key_sha256, label, scopes, org_id)
		VALUES ($1, $2, 'null-org', ARRAY['admin'::text], NULL)
	`, accts[0].accountID, []byte("00134-null-org"))
	if err == nil {
		t.Fatalf("INSERT with org_id NULL did not fail after the NOT NULL flip")
	}
	var nullErr *pgconn.PgError
	if !errors.As(err, &nullErr) {
		t.Fatalf("NULL org_id insert: got non-Postgres error: %v", err)
	}
	if nullErr.Code != "23502" {
		t.Errorf("NULL org_id insert: got SQLSTATE=%s, want 23502 (not_null_violation)", nullErr.Code)
	}

	// (3) The FK is ON DELETE RESTRICT, per pg_constraint. 'r' is
	// RESTRICT; 'c' (cascade) or 'n' (set null) would break the
	// GDPR-purge safety property the migration header calls out.
	var confDelType string
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype::text
		  FROM pg_constraint
		 WHERE conname = 'api_keys_org_id_fkey'
		   AND conrelid = 'api_keys'::regclass
	`).Scan(&confDelType); err != nil {
		t.Fatalf("api_keys_org_id_fkey probe: %v", err)
	}
	if confDelType != "r" {
		t.Errorf("api_keys_org_id_fkey confdeltype = %q, want \"r\" (ON DELETE RESTRICT; keys must block an org purge, never be silently deleted)", confDelType)
	}

	// (3b) …and the RESTRICT bites in practice: the org that still
	// owns a key cannot be deleted.
	_, err = pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, accts[0].orgID)
	if err == nil {
		t.Fatalf("DELETE of an org that still owns api_keys succeeded; ON DELETE RESTRICT regressed")
	}
	var restrictErr *pgconn.PgError
	if !errors.As(err, &restrictErr) {
		t.Fatalf("org delete: got non-Postgres error: %v", err)
	}
	if restrictErr.Code != "23503" || !strings.Contains(restrictErr.ConstraintName, "api_keys_org_id_fkey") {
		t.Errorf("org delete: got SQLSTATE=%s constraint=%q, want 23503 on api_keys_org_id_fkey",
			restrictErr.Code, restrictErr.ConstraintName)
	}

	// (4) A dangling org_id is rejected with 23503.
	_, err = pool.Exec(ctx, `
		INSERT INTO api_keys (account_id, key_sha256, label, scopes, org_id)
		VALUES ($1, $2, 'dangling-org', ARRAY['admin'::text],
		        '00000000-0000-0000-0000-0000000000aa')
	`, accts[0].accountID, []byte("00134-dangling-org"))
	if err == nil {
		t.Fatalf("INSERT with a dangling org_id succeeded; api_keys_org_id_fkey regressed")
	}
	var danglingErr *pgconn.PgError
	if !errors.As(err, &danglingErr) {
		t.Fatalf("dangling org_id insert: got non-Postgres error: %v", err)
	}
	if danglingErr.Code != "23503" || !strings.Contains(danglingErr.ConstraintName, "api_keys_org_id_fkey") {
		t.Errorf("dangling org_id insert: got SQLSTATE=%s constraint=%q, want 23503 on api_keys_org_id_fkey",
			danglingErr.Code, danglingErr.ConstraintName)
	}

	// (5) Every key resolves to its account's personal org — the
	// invariant the backfill established.
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
		t.Errorf("api_keys rows whose org_id does not match the account's personal org: %d", orphaned)
	}
}

func TestMigrations_00134_ReplaySafety(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}

	// One account with its personal org, plus a SHARED org the account
	// also holds a key in. The shared-org key is the tripwire: 00134's
	// backfill must never re-point it at the personal org.
	accountID := seedAccount(t, ctx, pool)
	personalOrgID := seedPersonalOrg(t, ctx, pool, accountID)
	sharedOrgID := seedOrg(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO org_memberships (org_id, account_id, role)
		VALUES ($1, $2, 'owner')
	`, sharedOrgID, accountID); err != nil {
		t.Fatalf("seed shared-org membership: %v", err)
	}

	keys := []struct {
		label string
		orgID string
	}{
		{"personal", personalOrgID},
		{"shared", sharedOrgID},
	}
	for _, k := range keys {
		if _, err := pool.Exec(ctx, `
			INSERT INTO api_keys (account_id, key_sha256, label, scopes, org_id)
			VALUES ($1, $2, $3, ARRAY['admin'::text], $4)
		`, accountID, []byte("00134-replay-"+k.label), k.label, k.orgID); err != nil {
			t.Fatalf("seed %s-org api_key: %v", k.label, err)
		}
	}

	// A second MigrateUp is a no-op (goose skips applied versions; the
	// migration body must not be re-runnable into an error either).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("second MigrateUp: %v", err)
	}

	// The real replay pin: run 00134's backfill UPDATE body verbatim
	// against rows that already carry an org_id. `WHERE k.org_id IS
	// NULL` must make it a zero-row no-op — without that guard the
	// shared-org key below would be silently re-pointed at the
	// account's personal org.
	tag, err := pool.Exec(ctx, backfill00134Body)
	if err != nil {
		t.Fatalf("replay of 00134 backfill body: %v", err)
	}
	if n := tag.RowsAffected(); n != 0 {
		t.Errorf("replayed 00134 backfill updated %d rows, want 0 (the `WHERE k.org_id IS NULL` guard is the replay-safety contract)", n)
	}
	for _, k := range keys {
		var got string
		if err := pool.QueryRow(ctx,
			`SELECT org_id::text FROM api_keys WHERE label = $1`, k.label).Scan(&got); err != nil {
			t.Fatalf("read %s-org key after replay: %v", k.label, err)
		}
		if got != k.orgID {
			t.Errorf("%s-org key org_id after replay = %s, want %s (backfill clobbered an already-stamped org)",
				k.label, got, k.orgID)
		}
	}

	// A third MigrateUp is still a no-op and leaves the row count alone.
	var pre int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM api_keys WHERE org_id IS NOT NULL`).Scan(&pre); err != nil {
		t.Fatalf("count pre: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("third MigrateUp: %v", err)
	}
	var post int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM api_keys WHERE org_id IS NOT NULL`).Scan(&post); err != nil {
		t.Fatalf("count post: %v", err)
	}
	if post != pre || post != len(keys) {
		t.Errorf("replay safety: pre=%d post=%d, want both %d", pre, post, len(keys))
	}
}
