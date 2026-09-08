//go:build !no_pg

// Migration-apply test for 00212_github_webhook_secrets.sql
// (PR-D / ADR-012 §7 amendment — per-tenant webhook secret).
//
// Pins:
//
//  1. Migration set applies cleanly through 00212 (no goose
//     duplicate-version panic). PR-D carries sibling fences
//     00207/00208/00210/00211 on the branch tip — those become
//     no-ops once the real migrations squash via `git rm` per
//     `git-mv-migration-internals-untouched`.
//  2. `github_webhook_secrets` table exists with four columns:
//     installation_id (bigint, PK), secret_value (bytea),
//     upgraded_at (timestamptz with default now()), upgraded_by
//     (text with default 'platform').
//  3. PK on installation_id is a single-column bigint — verified
//     via pg_constraint (NOT pg_index.indkey which is unstable
//     across PG versions per ADR-041).
//  4. default_branch column on github_installations still exists
//     (PR-D's Work item 3 is code-only; no schema change there).
//  5. Round-trip: insert a row, select it back, assert the bytea
//     round-trips with bytewise equality. Catches a future
//     refactor that silently re-encodes via hex/text.
//  6. Replay-safety: the apply_walk_test.go harness runs
//     MigrateUp twice. The migration's IF NOT EXISTS guard
//     makes the second pass a no-op; the harness fails loudly if
//     the second pass errors.
//  7. github_webhook_secret_changed pg_notify trigger is present
//     on github_webhook_secrets and fires after INSERT/UPDATE.
//     Inserted as the api/githubd cache-invalidation bridge
//     (cmd/githubd/main.go listens).
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

func TestMigrations_00212_GithubWebhookSecrets(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// (2) columns + types
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable, column_default IS NOT NULL
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'github_webhook_secrets'
		ORDER BY column_name
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	wantCols := map[string]struct {
		typ      string
		nullable string
		hasDef   bool
	}{
		"installation_id": {"bigint", "NO", false},
		"secret_value":    {"bytea", "NO", false},
		"upgraded_at":     {"timestamp with time zone", "NO", true},
		"upgraded_by":     {"text", "NO", true},
	}
	got := map[string]struct {
		typ      string
		nullable string
		hasDef   bool
	}{}
	for rows.Next() {
		var name, typ, nullable string
		var hasDef bool
		if err := rows.Scan(&name, &typ, &nullable, &hasDef); err != nil {
			rows.Close()
			t.Fatalf("scan column: %v", err)
		}
		got[name] = struct {
			typ      string
			nullable string
			hasDef   bool
		}{typ, nullable, hasDef}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	for col, want := range wantCols {
		actual, ok := got[col]
		if !ok {
			t.Errorf("github_webhook_secrets.%s: missing", col)
			continue
		}
		if actual.typ != want.typ {
			t.Errorf("github_webhook_secrets.%s: got data_type=%s, want %s", col, actual.typ, want.typ)
		}
		if actual.nullable != want.nullable {
			t.Errorf("github_webhook_secrets.%s: got nullable=%s, want %s", col, actual.nullable, want.nullable)
		}
		if actual.hasDef != want.hasDef {
			t.Errorf("github_webhook_secrets.%s: got hasDefault=%v, want %v", col, actual.hasDef, want.hasDef)
		}
	}

	// (3) PK on installation_id is a single-column bigint.
	// Lookup via pg_constraint so we don't depend on
	// pg_index.indkey which is unstable across PG versions per
	// ADR-041.
	pkRows, err := pool.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod)
		FROM pg_constraint c
		JOIN unnest(c.conkey) WITH ORDINALITY AS u(attnum, ord) ON TRUE
		JOIN pg_attribute a
		  ON a.attrelid = c.conrelid AND a.attnum = u.attnum
		WHERE c.conname = 'github_webhook_secrets_pkey'
		  AND c.conrelid = 'github_webhook_secrets'::regclass
		ORDER BY u.ord
	`)
	if err != nil {
		t.Fatalf("query pg_constraint for github_webhook_secrets_pkey: %v", err)
	}
	var pkCols []string
	for pkRows.Next() {
		var c, typ string
		if err := pkRows.Scan(&c, &typ); err != nil {
			pkRows.Close()
			t.Fatalf("scan pk column: %v", err)
		}
		pkCols = append(pkCols, c)
		// Confirm the column type too — PR-D pins the PK as bigint.
		if c == "installation_id" && typ != "bigint" {
			t.Errorf("github_webhook_secrets_pkey.installation_id: got type=%s, want bigint", typ)
		}
	}
	pkRows.Close()
	if err := pkRows.Err(); err != nil {
		t.Fatalf("pkRows.Err: %v", err)
	}
	wantPK := []string{"installation_id"}
	if len(pkCols) != len(wantPK) {
		t.Fatalf("github_webhook_secrets_pkey: got %v, want %v (PK must be a single-column on installation_id)", pkCols, wantPK)
	}
	for i, want := range wantPK {
		if pkCols[i] != want {
			t.Errorf("github_webhook_secrets_pkey column %d: got %q, want %q", i, pkCols[i], want)
		}
	}

	// (4) github_installations.default_branch still exists (PR-D
	// Work item 3 is code-only; the column was added long before
	// PR-D via schema.sql:974).
	dbrRows, err := pool.Query(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'github_installations'
		  AND column_name = 'default_branch'
	`)
	if err != nil {
		t.Fatalf("query github_installations.default_branch: %v", err)
	}
	if !dbrRows.Next() {
		dbrRows.Close()
		t.Fatalf("github_installations.default_branch missing — PR-D's Work item 3 expects this column to exist on the install row")
	}
	var dbrTyp, dbrNullable string
	if err := dbrRows.Scan(&dbrTyp, &dbrNullable); err != nil {
		dbrRows.Close()
		t.Fatalf("scan default_branch column: %v", err)
	}
	dbrRows.Close()
	if dbrTyp != "text" {
		t.Errorf("github_installations.default_branch: got data_type=%s, want text", dbrTyp)
	}
	if dbrNullable != "NO" {
		t.Errorf("github_installations.default_branch: got nullable=%s, want NO", dbrNullable)
	}

	// (5) bytea round-trip: insert a row, select it back, assert
	// bytewise equality on the secret_value. Catches a future
	// refactor that re-encodes via hex/text. The secret is a
	// random 32-byte sequence — anything outside the printable
	// ASCII range is intentional (guards the bytea path, not
	// the text path).
	want := []byte{0xff, 0x01, 0x7f, 0x42, 0x00, 0x13, 0xab, 0xcd, 0xef, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33,
		0x22, 0x11, 0x00, 0xfe, 0xdc, 0xba, 0x09, 0x87, 0x65, 0x43, 0x21, 0x10, 0x0f, 0x0e, 0x0d, 0x0c}
	_, err = pool.Exec(ctx, `
		INSERT INTO github_webhook_secrets (installation_id, secret_value, upgraded_by)
		VALUES ($1, $2, 'test')
	`, int64(42), want)
	if err != nil {
		t.Fatalf("insert test row: %v", err)
	}
	var got2 []byte
	if err := pool.QueryRow(ctx, `SELECT secret_value FROM github_webhook_secrets WHERE installation_id = $1`, int64(42)).Scan(&got2); err != nil {
		t.Fatalf("select test row: %v", err)
	}
	if len(got2) != len(want) {
		t.Fatalf("bytea round-trip: got %d bytes, want %d", len(got2), len(want))
	}
	for i := range want {
		if got2[i] != want[i] {
			t.Errorf("bytea round-trip: byte %d mismatch (got 0x%02x, want 0x%02x)", i, got2[i], want[i])
		}
	}
	// Cleanup so the test is idempotent within the same apply walk.
	_, _ = pool.Exec(ctx, `DELETE FROM github_webhook_secrets WHERE installation_id = $1`, int64(42))

	// (7) pg_notify trigger exists. The trigger is the
	// api/githubd cache-invalidation bridge: cmd/githubd/main.go
	// LISTENs on github_webhook_secret_changed and drops the
	// cached entry on every row INSERT/UPDATE. If the trigger is
	// missing, the daemon-side resolver's Invalidate() is dead
	// code (the only thing that would call it is pg_notify).
	var triggerName string
	err = pool.QueryRow(ctx, `
		SELECT trigger_name
		FROM information_schema.triggers
		WHERE event_object_schema = current_schema()
		  AND event_object_table   = 'github_webhook_secrets'
		  AND trigger_name         = 'github_webhook_secrets_notify_trg'
	`).Scan(&triggerName)
	if err != nil {
		t.Errorf("github_webhook_secrets_notify_trg missing: %v", err)
	}
}
