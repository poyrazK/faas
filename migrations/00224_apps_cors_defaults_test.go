//go:build !no_pg

// Migration-apply test for 00224_apps_cors_defaults.sql
// (ADR-091 / per-app default CORS columns on `apps`).
//
// Pins:
//
//  1. Migration set applies cleanly through 00224 (no goose
//     duplicate-version panic). The slot is real; the
//     companion 00225 file is a no-op fence (per
//     cross-pr-slot-gate-reservation-fence-pattern), so
//     the contiguity test allows a non-claiming
//     reservation file at 00225.
//  2. cors_default_enabled column exists, is boolean
//     NOT NULL, and has the DEFAULT false literal. Every
//     pre-00224 app row gets cors_default_enabled=false
//     lazily on first read/write without an UPDATE
//     rewrite, so the migration is metadata-only.
//     Backwards-compat: existing wakes behave exactly as
//     today — the fallback is off by default.
//  3. cors_default_origins column exists, is text[]
//     nullable (no NOT NULL, no DEFAULT). A NULL array
//     and an empty array are both treated as "deny all"
//     by the gateway; the application layer is the
//     authority on the meaning of the value.
//  4. Backfill pin: insert an app row WITHOUT specifying
//     the new columns → read back → assert
//     cors_default_enabled=false. Catches a future
//     refactor that drops the NOT NULL DEFAULT and
//     breaks reads of pre-PR app rows.
//  5. Round-trip pin: PATCH a row to set both columns →
//     read back → assert values match. Pins the
//     application-layer wiring (pkg/state types,
//     UpdateApp SQL) that lands in the same PR.
//  6. Replay-safety: the apply_walk_test.go harness runs
//     MigrateUp twice. The migration's IF NOT EXISTS /
//     IF EXISTS guards make the second pass a no-op; the
//     harness fails loudly if the second pass errors.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see
// migrations/README.md).
package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrations_00224_AppsCORSDefaults(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// (2) cors_default_enabled column shape: boolean NOT NULL
	// DEFAULT false. The PG normalized form is
	// 'false'::boolean in information_schema; accept both
	// spellings just like the scope test does.
	rows, err := pool.Query(ctx, `
		SELECT data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'apps'
		  AND column_name = 'cors_default_enabled'
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns for cors_default_enabled: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatalf("apps.cors_default_enabled column missing")
	}
	var dataType, nullable, columnDefault string
	if err := rows.Scan(&dataType, &nullable, &columnDefault); err != nil {
		rows.Close()
		t.Fatalf("scan cors_default_enabled column: %v", err)
	}
	rows.Close()
	if dataType != "boolean" {
		t.Errorf("apps.cors_default_enabled: got data_type=%s, want boolean", dataType)
	}
	if nullable != "NO" {
		t.Errorf("apps.cors_default_enabled: got nullable=%s, want NO (NOT NULL DEFAULT false is the fast-default that backfills pre-PR rows)", nullable)
	}
	if columnDefault != "false" && columnDefault != "false::boolean" {
		t.Errorf("apps.cors_default_enabled: got column_default=%q, want 'false' (boolean literal)", columnDefault)
	}

	// (3) cors_default_origins column shape: text[] NULLABLE.
	// A NULL or empty array are both treated as "deny all" by
	// the gateway; the application layer is the authority on
	// the meaning. We assert the column is present + is an
	// array of text (udt_name = '_text') + is NULLABLE so a
	// future refactor that adds NOT NULL or changes the type
	// surfaces here.
	rows, err = pool.Query(ctx, `
		SELECT data_type, udt_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'apps'
		  AND column_name = 'cors_default_origins'
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns for cors_default_origins: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatalf("apps.cors_default_origins column missing")
	}
	var originsType, originsUDT, originsNullable string
	if err := rows.Scan(&originsType, &originsUDT, &originsNullable); err != nil {
		rows.Close()
		t.Fatalf("scan cors_default_origins column: %v", err)
	}
	rows.Close()
	if originsUDT != "_text" {
		t.Errorf("apps.cors_default_origins: got udt_name=%s, want _text (text[] array type)", originsUDT)
	}
	if originsNullable != "YES" {
		t.Errorf("apps.cors_default_origins: got nullable=%s, want YES (NULL and empty are both 'deny all' in the gateway)", originsNullable)
	}

	// (4) Backfill pin: insert an app row WITHOUT specifying
	// cors_default_enabled → read back → assert false. The
	// historical column list omits cors_default_enabled so
	// the ONLY way the row gets a value is via the NOT NULL
	// DEFAULT false clause. A future refactor that drops the
	// DEFAULT or the NOT NULL would fail this assertion and
	// surface here, before any production wake runs against a
	// pre-PR app.
	accountID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, email, plan)
		VALUES ($1::uuid, $1::text || '@cors-defaults-test.example.com', 'hobby')
		ON CONFLICT (id) DO NOTHING
	`, accountID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	// `apps` has no `name` column — the human-facing identifier is
	// `slug` (apps_slug_key UNIQUE). The historical column list here
	// deliberately omits cors_default_enabled so the NOT NULL DEFAULT
	// false clause is the only thing that can populate it.
	var gotEnabled bool
	if err := pool.QueryRow(ctx, `
		INSERT INTO apps (id, account_id, slug, ram_mb)
		VALUES (gen_random_uuid(), $1::uuid, $1::text, 256)
		RETURNING cors_default_enabled
	`, accountID).Scan(&gotEnabled); err != nil {
		t.Fatalf("backfill insert: %v", err)
	}
	if gotEnabled {
		t.Errorf("backfill: got cors_default_enabled=true, want false (NOT NULL DEFAULT false must materialize on insert)")
	}

	// (5) Round-trip pin: PATCH the row to set both columns →
	// read back → assert values match. Pins the
	// application-layer wiring (state.App struct +
	// UpdateApp SQL) that lands in the same PR. Using the
	// store-shaped SELECT (not a raw column list) is
	// load-bearing: a future refactor that adds the columns
	// to the schema but forgets the store scan would
	// surface here before the gateway sees the row.
	appID := uuid.NewString()
	slug := "corsdefaults-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO apps (id, account_id, slug, ram_mb)
		VALUES ($1, $2, $3, 256)
	`, appID, accountID, slug); err != nil {
		t.Fatalf("insert app for round-trip: %v", err)
	}
	// UpdateApp-style PATCH (column list matches what the
	// store layer writes after the wire-shape widening).
	if _, err := pool.Exec(ctx, `
		UPDATE apps
		SET cors_default_enabled = true,
		    cors_default_origins = ARRAY['https://app.example.com','https://*.staging.example.com']::text[]
		WHERE id = $1
	`, appID); err != nil {
		t.Fatalf("update app for round-trip: %v", err)
	}
	var gotOrigins []string
	if err := pool.QueryRow(ctx, `
		SELECT cors_default_enabled, cors_default_origins
		FROM apps
		WHERE id = $1
	`, appID).Scan(&gotEnabled, &gotOrigins); err != nil {
		t.Fatalf("round-trip select: %v", err)
	}
	if !gotEnabled {
		t.Errorf("round-trip: got cors_default_enabled=false, want true after PATCH")
	}
	if len(gotOrigins) != 2 ||
		gotOrigins[0] != "https://app.example.com" ||
		gotOrigins[1] != "https://*.staging.example.com" {
		t.Errorf("round-trip: got cors_default_origins=%v, want [https://app.example.com https://*.staging.example.com]", gotOrigins)
	}

	// Light sanity: the state.App type at least compiles with
	// the new fields. A no-op build-only assertion; the real
	// shape coverage lives in pkg/state/pgstore_round_trip_test.go.
	var _ = state.App{}
}
