//go:build !no_pg

// Migration-apply test for 00159 (issue #315 / tier-2 DX — adds
// the new 'replay' source value to invocations_source_check).
//
// Pins the load-bearing contract for the replay path:
//
//  1. The migration set applies cleanly through 00159.
//  2. The CHECK constraint accepts source='replay' on a fresh row.
//  3. The CHECK still rejects a stray source value (closed-set
//     vocabulary must not regress to 'TEXT').
//  4. Re-running goose MigrateUp is a no-op (idempotent replay
//     safety — the apply_walk_test pins this at the directory level
//     but per-migration shape is asserted here as defence in depth).
//
// Build tag mirrors 00025_deployments_rootfs_key_test.go: set
// FAAS_SKIP_PG_TESTS=1 locally to skip.

package migrations_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00159_InvocationsReplaySource is the per-migration
// pin for the 'replay' source value added to invocations_source_check
// (issue #315).
func TestMigrations_00159_InvocationsReplaySource(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00159 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 158 and 159)", err)
	}

	// (2) Closed-set CHECK shape. pg_get_constraintdef emits either
	// IN (a, b, c) or ANY(ARRAY[a, b, c]) per
	// pg-get-constraintdef-shapes.md; we just assert the closed-set
	// vocabulary is present.
	var def string
	err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'invocations_source_check'
		   and n.nspname = current_schema()`).Scan(&def)
	if err != nil {
		t.Fatalf("query constraint: %v (closed-set CHECK must have landed)", err)
	}
	for _, want := range []string{"async_invoke", "queue", "delayed_task", "cron", "replay"} {
		if !strings.Contains(def, want) {
			t.Errorf("constraint def %q missing closed-set value %q", def, want)
		}
	}

	// (3) Insert with source='replay' must succeed. invocations
	// carries FKs to accounts + apps, so seed both parents first;
	// the row IDs are freshly minted so a parallel pgtest run on the
	// same box doesn't collide (migrations-public-prefix-race.md).
	invID := randomUUID(t)
	acctID := randomUUID(t)
	appID := randomUUID(t)
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1::uuid, $2, 'scale', now())`,
		acctID, acctID+"@example.com"); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ($1::uuid, $2::uuid, 'invocations-replay-source-test', 'app', 128, 1, 'active', now())`,
		appID, acctID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	_, err = pool.Exec(ctx, `
		insert into invocations
		    (id, app_id, account_id, source, state, method, path, due_at, created_at)
		values ($1::uuid, $2::uuid, $3::uuid, 'replay', 'pending', 'POST', '/', now(), now())`,
		invID, appID, acctID)
	if err != nil {
		t.Fatalf("insert with source='replay' failed: %v (CHECK must allow 'replay')", err)
	}

	// (4) Insert with a bogus source must still fail. The constraint
	// is a closed-set; if the migration accidentally dropped it
	// (or replaced it with a permissive text column) this assertion
	// would surface the regression.
	_, err = pool.Exec(ctx, `
		insert into invocations
		    (id, app_id, account_id, source, state, method, path, due_at, created_at)
		values ($1::uuid, $2::uuid, $3::uuid, 'bogus', 'pending', 'POST', '/', now(), now())`,
		randomUUID(t), appID, acctID)
	if err == nil {
		t.Fatalf("insert with source='bogus' succeeded; CHECK is missing or non-closed")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("insert 'bogus' returned non-PgError: %v", err)
	}
	if pgErr.Code != "23514" { // check_violation
		t.Errorf("bogus source error code = %s, want 23514 (check_violation)", pgErr.Code)
	}

	// (5) Replay safety: applying the migration set a second time
	// must not blow up. The DROP CONSTRAINT IF EXISTS guard
	// handles this; this assertion is a tripwire that survives
	// future refactors.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (the DROP CONSTRAINT IF EXISTS guard must have been silently dropped)", err)
	}
}

// randomUUID returns a hex-encoded UUIDv4-style 32-char string for
// pgtest row inserts. Uses crypto/rand (not math/rand) so a
// parallel test run on the same box can't collide.
func randomUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b[:])
}
