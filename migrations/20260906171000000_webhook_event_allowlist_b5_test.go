//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_WebhookEventAllowlistB5(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	const (
		acctID = "00000000-0000-0000-0000-000000001395"
		appID  = "00000000-0000-0000-0000-000000001396"
		hookID = "00000000-0000-0000-0000-000000001397"
	)
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, $2, 'scale', now()) on conflict (id) do nothing
	`, acctID, acctID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ($1, $2, 'webhook-b5', 'app', 128, 1, 'active', now())
		on conflict (id) do nothing
	`, appID, acctID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into app_webhooks (id, app_id, account_id, target_url, secret_sealed)
		values ($1, $2, $3, 'https://example.com/b5', '\x00'::bytea)
		on conflict (id) do nothing
	`, hookID, appID, acctID); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	for _, event := range []string{
		"deployment.failed", "rollout.aborted", "error.new",
		"job.finished", "preview.created", "budget.threshold",
	} {
		if _, err := pool.Exec(ctx, `
			insert into app_webhook_deliveries
				(webhook_id, app_id, account_id, event, payload)
			values ($1, $2, $3, $4, '{}'::jsonb)
		`, hookID, appID, acctID, event); err != nil {
			t.Errorf("event=%q should be accepted: %v", event, err)
		}
	}
	_, err := pool.Exec(ctx, `
		insert into app_webhook_deliveries
			(webhook_id, app_id, account_id, event, payload)
		values ($1, $2, $3, 'deployment.faild', '{}'::jsonb)
	`, hookID, appID, acctID)
	var pgErr *pgconn.PgError
	if err == nil || !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("typo event should fail with check_violation, got %v", err)
	}
}
