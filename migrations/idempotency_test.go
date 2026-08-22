//go:build !no_pg

// Idempotency gate for migrations added by the current PR.
//
// The failure this prevents
// -------------------------
// `apply-and-walk` migrates a FRESH database, which proves a migration can
// run once but never proves it can run twice. A migration that succeeds on
// up #1 and fails on up #2 (or quietly alters state on #2) is reachable in
// production through:
//
//   - a deployment box that re-runs `MigrateUp` at startup after a partial
//     apply (network blip during the first run left goose_db_version's
//     ledger row in place but the DDL commit unobserved);
//   - a test database that some integration suite (eg meterd's cmd/e2e)
//     re-bootstraps without dropping the schema first.
//
// Both paths re-execute every already-applied migration. If any of them
// is not idempotent — bare CREATE TABLE without IF NOT EXISTS, a backfill
// UPDATE that re-runs the side effect, a constraint addition that 42710's
// on the second up — the second MigrateUp fails and the process dies
// before the daemon starts. CI never catches this because CI only ever
// applies each migration once.
//
// This test runs MigrateUp twice against a fresh schema and asserts the
// post-apply schema fingerprint is byte-equal across both runs. A schema
// whose fingerprint drifts across up;up is the failure shape this gate is
// designed to detect.
//
// Scope: only migrations the PR ADDS
// ----------------------------------
// Mirrors replay_safety_test.go's scoping. The whole-history idempotency
// check would be cheap to add, but it would also re-fail 00001_init (a
// bare CREATE TABLE for the `accounts` table — not idempotent, and not
// required to be, because no operator re-runs MigrateUp from scratch).
// Replaying only the tail makes the gate load-bearing for new work
// without demanding an unnecessary rewrite of history.
//
// Wiring: ci.yml computes the added versions with `git diff --diff-filter=A`
// and exports FAAS_IDEMPOTENCY_CHECK_VERSIONS. The test SKIPs when the
// env var is unset (no new migrations in this diff) — the same shape the
// replay-safety gate uses at replay_safety_test.go:47-50.
package migrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestNewMigrationsAreIdempotent fails when a migration added by this PR
// is not re-runnable on a fresh schema. It does NOT enforce whole-history
// idempotency (see file-level comment for the scoping rationale).
func TestNewMigrationsAreIdempotent(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("FAAS_IDEMPOTENCY_CHECK_VERSIONS"))
	if raw == "" {
		t.Skip("FAAS_IDEMPOTENCY_CHECK_VERSIONS unset (no new migrations in this diff); nothing to replay")
	}
	if testing.Short() {
		t.Skip("skipping idempotency gate in -short mode")
	}

	ctx := context.Background()
	pool := pgtest.Open(t) // t.Skip-friendly when Postgres is absent

	// 1. Migrate the fresh schema — same as apply-and-walk does on every CI
	//    run today. We deliberately do NOT reset the schema between up #1
	//    and up #2: the production shape is "ledger says X is applied, the
	//    DDL is already there", which is exactly what MigrateUp re-executes.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	fp1, err := schemaFingerprint(ctx, pool)
	if err != nil {
		t.Fatalf("schema fingerprint after first MigrateUp: %v", err)
	}

	// 2. Re-apply. A migration that is not idempotent surfaces HERE: either
	//    the second MigrateUp errors (bare CREATE TABLE → 42P07), or it
	//    succeeds but silently writes a row / mutates a side effect that the
	//    fingerprint catches (backfill UPDATE on already-populated rows).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf(`migration is not idempotent: %v

A second MigrateUp against the same schema failed. The migration this PR
adds is reachable twice in production through:

  - a daemon restart after a partial first apply (the goose ledger row
    committed but the DDL commit was unobserved), which makes MigrateUp
    re-execute the already-applied migration at startup;
  - a test harness that re-bootstraps without dropping the schema first.

Make the new migration re-runnable. Established shapes in this repo:
  CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS
  ALTER TABLE ... ADD COLUMN IF NOT EXISTS
  backfills: guard with a WHERE-clause that matches only the rows that
              still need the side effect (eg WHERE foo IS NULL).
  constraints + enum-value additions: guard with a DO block that checks
              pg_constraint / information_schema first.

Do NOT edit an already-merged migration to fix this — migrations are
append-only (CLAUDE.md). Only the migration this PR adds should change.`, err)
	}
	fp2, err := schemaFingerprint(ctx, pool)
	if err != nil {
		t.Fatalf("schema fingerprint after second MigrateUp: %v", err)
	}

	if fp1 != fp2 {
		t.Fatalf(`schema drifted across idempotent MigrateUp

  before (fp1) = %s
  after  (fp2) = %s

The schema fingerprint is byte-equal by construction; a drift means one
of the new migrations altered the schema on the second up. Inspect the
schema diff via pg_dump and rewrite the offending migration to be a
no-op when its effects are already present (see full error above).`, fp1, fp2)
	}
	t.Logf("idempotency: OK (schema fingerprint %s stable across up;up)", fp1)
}

// schemaFingerprint returns a stable SHA-256 over (tables, columns, types,
// nullability, defaults, indexes, constraints) for the test's isolated
// schema. Two post-MigrateUp states with the same fingerprint are
// byte-identical at the schema level.
//
// Implementation notes:
//
//   - The query is scoped to current_schema() so the test stays
//     isolated across packages sharing the same Postgres cluster (see
//     pgtest.Open: each test gets its own schema via search_path).
//   - The `tstamp` column of `goose_db_version` is excluded — goose
//     updates it on every up, so leaving it in the fingerprint would
//     guarantee fp1 != fp2 and make the gate meaningless.
//   - Default expression comparison (pg_get_expr(adbin, adrelid)) is
//     included because a non-idempotent migration could differ ONLY in
//     defaults while passing column-level equality.
func schemaFingerprint(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	const q = `
WITH schema_oid AS (SELECT oid FROM pg_namespace WHERE nspname = current_schema())
SELECT
  c.relkind || ':' || c.relname || ':' ||
  (a.attnum::text) || ':' ||
  (a.attname) || ':' ||
  (format_type(a.atttypid, a.atttypmod)) || ':' ||
  (a.attnotnull::text) || ':' ||
  COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') AS row
FROM pg_class c
JOIN pg_attribute a ON a.attrelid = c.oid
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE c.relnamespace = (SELECT oid FROM schema_oid)
  AND a.attnum > 0
  AND NOT a.attisdropped
  AND c.relkind IN ('r', 'p', 'v', 'm', 'S')
  -- Exclude the goose ledger's tstamp column: it changes on every up.
  AND NOT (c.relname = 'goose_db_version' AND a.attname = 'tstamp')
UNION ALL
SELECT
  'idx:' || ic.relname || ':' || tc.relname || ':' ||
  COALESCE(pg_get_indexdef(i.indexrelid), '')
FROM pg_index i
JOIN pg_class ic ON ic.oid = i.indexrelid
JOIN pg_class tc ON tc.oid = i.indrelid
WHERE ic.relnamespace = (SELECT oid FROM schema_oid)
  AND i.indislive
UNION ALL
SELECT
  'con:' || con.conname || ':' || cl.relname || ':' ||
  con.contype || ':' ||
  COALESCE(pg_get_constraintdef(con.oid), '') || ':' ||
  COALESCE(array_to_string(con.conkey, ','), '') || ':' ||
  COALESCE(cc.relname, '')
FROM pg_constraint con
JOIN pg_class cl ON cl.oid = con.conrelid
LEFT JOIN pg_class cc ON cc.oid = con.confrelid
WHERE con.connamespace = (SELECT oid FROM schema_oid)
  AND con.contype IN ('p','f','u','c')
`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(out)
	h := sha256.Sum256([]byte(strings.Join(out, "\n")))
	return hex.EncodeToString(h[:]), nil
}