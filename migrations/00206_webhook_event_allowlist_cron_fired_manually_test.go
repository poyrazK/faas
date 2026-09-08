//go:build !no_pg

// Migration-apply test for 00206_webhook_event_allowlist_cron_fired_manually.sql
// (ADR-090 PR-D / issue #791 follow-up).
//
// Pins:
//
//  1. Migration set applies cleanly through 00206 against main's
//     00194_cron_fire_now_requests.sql (no goose duplicate-version
//     panic).
//  2. `app_webhook_deliveries_event_chk` accepts the new audit event
//     name `cron.fired.manually` (positive round-trip).
//  3. The pre-existing `cron.fired` vocabulary still accepts (regression
//     guard — old subscribers unaffected).
//  4. A typo (`cron.fired.manual`) is still rejected with pgx 23514
//     (check_violation) — the CHECK is closed and ordered, not
//     over-tolerant.
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

func TestMigrations_00206_WebhookEventAllowlist_CronFiredManually(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed the bare-minimum rows so the FK constraints from
	// app_webhook_deliveries hold: account → app → webhook. We don't
	// inspect the dispatcher's downstream claims — only the CHECK
	// shape — so one minimal row per parent table plus a non-null
	// payload + status is all the test needs.
	acctID := "00000000-0000-0000-0000-000000000003"
	appID := "00000000-0000-0000-0000-000000000002"
	hookID := "00000000-0000-0000-0000-000000000001"

	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'cron-fired-manually-test@example.com', 'scale', now())
		on conflict (id) do nothing
	`, acctID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ($1, $2, 'cron-fired-manually-test', 'app', 128, 1, 'active', now())
		on conflict (id) do nothing
	`, appID, acctID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into app_webhooks (id, app_id, account_id, target_url, secret_sealed)
		values ($1, $2, $3, 'https://example.com/cron-fired-manually', '\x00'::bytea)
		on conflict (id) do nothing
	`, hookID, appID, acctID); err != nil {
		t.Fatalf("seed app_webhooks: %v", err)
	}

	// (1) New event name must be accepted by the CHECK. The migration
	// widens the vocab to include `cron.fired.manually`, mirroring
	// the `cron.fired` constant in pkg/sched/loop.go.
	_, err := pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload, status)
		values ($1, $2, $3, 'cron.fired.manually', '{}'::jsonb, 'pending')
	`, hookID, appID, acctID)
	if err != nil {
		t.Fatalf("event='cron.fired.manually' should be accepted by the widened CHECK, got: %v", err)
	}

	// (2) The pre-existing `cron.fired` vocabulary still works.
	// Pin: a forward-only widening that drops existing rows' coverage
	// would be a regression. The DROP+ADD+VALIDATE triplet in 00206
	// uses the wider set in step (2) and the existing rows are all
	// within the new set by construction.
	_, err = pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload, status)
		values ($1, $2, $3, 'cron.fired', '{}'::jsonb, 'pending')
	`, hookID, appID, acctID)
	if err != nil {
		t.Errorf("event='cron.fired' should still be accepted (regression), got: %v", err)
	}

	// (3) Typo rejection — the CHECK is closed, not a prefix match.
	// 'cron.fired.manual' (singular, missing 'ly') must still 23514.
	_, err = pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload, status)
		values ($1, $2, $3, 'cron.fired.manual', '{}'::jsonb, 'pending')
	`, hookID, appID, acctID)
	var pgErr *pgconn.PgError
	if err == nil {
		t.Errorf("event='cron.fired.manual' (typo) should be rejected by the closed-vocab CHECK")
	} else if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("event='cron.fired.manual' error = %v, want pgx 23514 (check_violation)", err)
	}
}
