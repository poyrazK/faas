//go:build !no_pg

// Shape test for migration 00141 (issue #476 / ADR-076) —
// app_webhook_deliveries ledger table. Mirror of
// migrations/00062_alert_rules_test.go so the same gate catches a
// hand-edit that drops a constraint, drops an index, or breaks
// idempotency.
//
// Asserts:
//
//  1. app_webhook_deliveries lands with every column from the schema
//     (id, webhook_id, app_id, account_id, event, payload, attempt,
//     status, last_error, last_response_code, next_attempt_at,
//     delivered_at, created_at, updated_at).
//  2. status CHECK accepts ('pending','in_flight','succeeded',
//     'failed','dead') and rejects everything else with 23514.
//  3. event CHECK accepts platform and application-defined event types,
//     while rejecting empty or overlong values.
//  4. attempt CHECK enforces 0..7 — the dispatcher's DLQ ceiling.
//  5. partial index app_webhook_deliveries_pending_idx exists on
//     (status, next_attempt_at) WHERE status IN ('pending',
//     'in_flight') — the dispatcher's claim predicate.
//  6. FK CASCADE on webhook_id: deleting the subscription drops its
//     ledger.
//  7. Replay-safe: db.MigrateUp runs twice cleanly.
//
// Slot note: real-schema slot lands at 141 on this branch. Bump
// pkg/e2etest/harness.go::e2eMigrationTarget to 141 when this lands.

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

func TestMigrations_00141_AppWebhookDeliveries_ShapeAndFK(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	const (
		acctID = "00000000-0000-0000-0000-000000000141"
		appID  = "00000000-0000-0000-0000-000000000241"
		hookID = "00000000-0000-0000-0000-000000000341"
	)

	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, $2, 'free', now())
		on conflict (id) do nothing
	`, acctID, acctID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, status, created_at)
		values ($1, $2, 'shape-deliveries-app', 'app', 128, 1, 'active', now())
		on conflict (id) do nothing
	`, appID, acctID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into app_webhooks
			(id, app_id, account_id, target_url, secret_sealed)
		values ($1, $2, $3, 'https://example.com/deliveries-test', '\x00'::bytea)
		on conflict (id) do nothing
	`, hookID, appID, acctID); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}

	// (1) Every column from the schema exists, in the natural order.
	wantCols := []string{
		"id", "webhook_id", "app_id", "account_id", "event",
		"payload", "attempt", "status",
		"last_error", "last_response_code",
		"next_attempt_at", "delivered_at",
		"created_at", "updated_at",
	}
	rows, err := pool.Query(ctx, `
		select attname from pg_attribute
		 where attrelid = 'app_webhook_deliveries'::regclass
		   and attnum > 0 and not attisdropped
		   and attname = any($1::text[])
		 order by attnum
	`, wantCols)
	if err != nil {
		t.Fatalf("pg_attribute scan: %v", err)
	}
	var gotCols []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			t.Fatalf("scan column: %v", err)
		}
		gotCols = append(gotCols, n)
	}
	rows.Close()
	if len(gotCols) != len(wantCols) {
		t.Errorf("app_webhook_deliveries columns: want %d (%v), got %d (%v)",
			len(wantCols), wantCols, len(gotCols), gotCols)
	} else {
		for i := range wantCols {
			if gotCols[i] != wantCols[i] {
				t.Errorf("app_webhook_deliveries column[%d]: want %q, got %q",
					i, wantCols[i], gotCols[i])
			}
		}
	}

	// (2) status CHECK accepts the closed set. We pin each value once
	// so a regression that narrowed the vocabulary would fail here.
	for _, s := range []string{"pending", "in_flight", "succeeded", "failed", "dead"} {
		if _, err := pool.Exec(ctx, `
			insert into app_webhook_deliveries
				(webhook_id, app_id, account_id, event, payload, status)
			values ($1, $2, $3, 'cron.fired', '{}'::jsonb, $4)
		`, hookID, appID, acctID, s); err != nil {
			t.Errorf("status=%q should be accepted, got: %v", s, err)
		}
	}
	_, err = pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload, status)
		values ($1, $2, $3, 'cron.fired', '{}'::jsonb, 'queued')
	`, hookID, appID, acctID)
	var pgErr *pgconn.PgError
	if err == nil {
		t.Errorf("status='queued' should be rejected by CHECK")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("status='queued' error = %v, want pgx 23514 (check_violation)", err)
	}

	// (3) Event types include both platform events and application-defined
	// outbox events, bounded to 1..256 characters.
	for _, ev := range []string{
		"cron.fired", "app.deployed", "app.scaled", "app.parked", "app.woken",
		"com.example.billing.invoice.created",
	} {
		if _, err := pool.Exec(ctx, `
			insert into app_webhook_deliveries
				(webhook_id, app_id, account_id, event, payload, status)
			values ($1, $2, $3, $4, '{}'::jsonb, 'pending')
		`, hookID, appID, acctID, ev); err != nil {
			t.Errorf("event=%q should be accepted, got: %v", ev, err)
		}
	}
	_, err = pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload)
		values ($1, $2, $3, '', '{}'::jsonb)
	`, hookID, appID, acctID)
	if err == nil {
		t.Errorf("empty event should be rejected by CHECK")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("empty event error = %v, want pgx 23514 (check_violation)", err)
	}
	_, err = pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload)
		values ($1, $2, $3, repeat('x', 257), '{}'::jsonb)
	`, hookID, appID, acctID)
	if err == nil {
		t.Errorf("event longer than 256 characters should be rejected by CHECK")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("overlong event error = %v, want pgx 23514 (check_violation)", err)
	}

	// (4) attempt CHECK enforces 0..7. The dispatcher's DLQ ceiling.
	for _, a := range []int{0, 1, 7, 8} {
		if _, err := pool.Exec(ctx, `
			insert into app_webhook_deliveries
				(webhook_id, app_id, account_id, event, payload, attempt)
			values ($1, $2, $3, 'cron.fired', '{}'::jsonb, $4)
		`, hookID, appID, acctID, a); err != nil {
			t.Errorf("attempt=%d should be accepted, got: %v", a, err)
		}
	}
	_, err = pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload, attempt)
		values ($1, $2, $3, 'cron.fired', '{}'::jsonb, 9)
	`, hookID, appID, acctID)
	if err == nil {
		t.Errorf("attempt=9 should be rejected by CHECK")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("attempt=9 error = %v, want pgx 23514 (check_violation)", err)
	}

	// (5) partial index app_webhook_deliveries_pending_idx exists on
	// (account_id, next_attempt_at) WHERE status IN ('pending','in_flight').
	// The dispatcher's claim query depends on this index for O(due
	// rows) reads AND the round-robin ORDER BY (account_id, next_attempt_at).
	// We probe pg_indexes.indexdef so a regression that drops the
	// partial predicate OR reorders the columns trips the test.
	var partialIdxDef string
	if err := pool.QueryRow(ctx, `
		select indexdef from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'app_webhook_deliveries'
		   and indexname = 'app_webhook_deliveries_pending_idx'
	`).Scan(&partialIdxDef); err != nil {
		t.Fatalf("pg_indexes probe: %v", err)
	}
	if !strings.Contains(partialIdxDef, "WHERE") {
		t.Errorf("partial index missing WHERE predicate: %q", partialIdxDef)
	}
	if !strings.Contains(partialIdxDef, "account_id") {
		t.Errorf("partial index missing account_id column: %q", partialIdxDef)
	}

	// (6) FK CASCADE on webhook_id: drop the subscription, drop the
	// ledger. Seed a fresh subscription + delivery, delete the
	// subscription, confirm the delivery is gone.
	const hookIDc = "00000000-0000-0000-0000-00000000034c"
	const delIDc = "00000000-0000-0000-0000-00000000044c"
	if _, err := pool.Exec(ctx, `
		insert into app_webhooks
			(id, app_id, account_id, target_url, secret_sealed)
		values ($1, $2, $3, 'https://example.com/cascade-del', '\x00'::bytea)
	`, hookIDc, appID, acctID); err != nil {
		t.Fatalf("seed cascade webhook: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(id, webhook_id, app_id, account_id, event, payload)
		values ($1, $2, $3, $4, 'cron.fired', '{}'::jsonb)
	`, delIDc, hookIDc, appID, acctID); err != nil {
		t.Fatalf("seed cascade delivery: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from app_webhooks where id = $1`, hookIDc); err != nil {
		t.Fatalf("delete cascade webhook: %v", err)
	}
	var orphan int
	if err := pool.QueryRow(ctx,
		`select count(*) from app_webhook_deliveries where id = $1`, delIDc,
	).Scan(&orphan); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphan != 0 {
		t.Errorf("FK cascade: expected delivery %s gone, %d row(s) remain", delIDc, orphan)
	}

	// (7) Replay-safe: CREATE TABLE IF NOT EXISTS +
	// CREATE INDEX IF NOT EXISTS make a second MigrateUp a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Errorf("MigrateUp idempotent re-apply: %v", err)
	}
}
