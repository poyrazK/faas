//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 102
//
// TestStreamingPlanGuardDoesNotBlockForeignKeyChecks pins H9-138: an app
// update on a streaming-enabled app locked the account FOR UPDATE, which
// blocks the FOR KEY SHARE lock a foreign-key check takes, so per-request
// request_id_journal inserts deadlocked with deploys on production-us. A
// genuine opt-in must still serialize with an account downgrade.
func TestStreamingPlanGuardDoesNotBlockForeignKeyChecks(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	accountID, streamingApp, plainApp := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE account_id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	mustExec(t, pool, `INSERT INTO accounts (id, email, plan, created_at) VALUES ($1, $2, 'pro', now())`,
		accountID, "streaming-guard-"+accountID+"@example.com")
	mustExec(t, pool, `INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, streaming_enabled, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1, 'active', true, now())`, streamingApp, accountID, "stream-"+streamingApp[:8])
	mustExec(t, pool, `INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1, 'active', now())`, plainApp, accountID, "plain-"+plainApp[:8])

	// journalBlocked reports whether a request-journal insert waits on an
	// in-flight UpdateApp-shaped update of the streaming app.
	journalBlocked := func() bool {
		t.Helper()
		update, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = update.Rollback(ctx) }()
		if _, err := update.Exec(ctx, `UPDATE apps SET ram_mb = 256, streaming_enabled = streaming_enabled WHERE id = $1`, streamingApp); err != nil {
			t.Fatalf("app update: %v", err)
		}
		// request_id_journal's account key references resource_namespaces
		// since #4882, so a session insert stands in for the foreign-key check
		// that still takes FOR KEY SHARE on the accounts row.
		return lockTimesOut(ctx, t, pool, `INSERT INTO sessions (account_id) VALUES ($1)`, accountID)
	}
	if journalBlocked() {
		t.Fatal("a request-journal insert waited on an app update; foreign-key checks must not queue behind the streaming guard")
	}

	// A real opt-in still takes the account lock a downgrade needs.
	optIn, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := optIn.Exec(ctx, `UPDATE apps SET streaming_enabled = true WHERE id = $1`, plainApp); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	if !lockTimesOut(ctx, t, pool, `UPDATE accounts SET plan = 'free' WHERE id = $1`, accountID) {
		t.Fatal("an account downgrade did not wait for a concurrent streaming opt-in")
	}
	_ = optIn.Rollback(ctx)

	// The previous guard is the bug: Down restores it and the insert waits.
	data, err := migrations.FS.ReadFile("20261012103729543_streaming_plan_guard_lock.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "-- +goose Down", 2)
	mustExec(t, pool, parts[1])
	if !journalBlocked() {
		t.Fatal("the pre-fix guard no longer reproduces the wait; this test would not catch a regression")
	}
	for range 2 {
		mustExec(t, pool, parts[0])
	}
	if journalBlocked() {
		t.Fatal("replaying Up did not restore the non-blocking guard")
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %.60q: %v", sql, err)
	}
}

// lockTimesOut runs sql in its own transaction with a short lock_timeout and
// reports whether it had to give up waiting for a lock.
func lockTimesOut(parent context.Context, t *testing.T, pool *pgxpool.Pool, sql string, args ...any) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '1s'`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, sql, args...)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
		return true
	}
	if err != nil && !strings.Contains(err.Error(), "cannot downgrade to Free") {
		t.Fatalf("exec %.60q: %v", sql, err)
	}
	return false
}
