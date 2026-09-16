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

func TestMigrations_AppWebhooksDeliveryFormat(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	const (
		acctID = "00000000-0000-0000-0000-00000000013c"
		appID  = "00000000-0000-0000-0000-00000000023c"
	)
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, $2, 'pro', now())
		on conflict (id) do nothing
	`, acctID, "cloudevents-migration@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ($1, $2, 'cloudevents-migration', 'app', 128, 1, 'active', now())
		on conflict (id) do nothing
	`, appID, acctID); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	var format string
	if err := pool.QueryRow(ctx, `
		insert into app_webhooks (app_id, account_id, target_url, secret_sealed)
		values ($1, $2, 'https://example.com/default-format', '\\x00'::bytea)
		returning delivery_format
	`, appID, acctID).Scan(&format); err != nil {
		t.Fatalf("default delivery_format: %v", err)
	}
	if format != "json" {
		t.Fatalf("default delivery_format = %q, want json", format)
	}

	if _, err := pool.Exec(ctx, `
		insert into app_webhooks (app_id, account_id, target_url, secret_sealed, delivery_format)
		values ($1, $2, 'https://example.com/cloud-format', '\\x00'::bytea, 'cloudevents')
	`, appID, acctID); err != nil {
		t.Fatalf("cloudevents delivery_format should be accepted: %v", err)
	}
	_, err := pool.Exec(ctx, `
		insert into app_webhooks (app_id, account_id, target_url, secret_sealed, delivery_format)
		values ($1, $2, 'https://example.com/invalid-format', '\\x00'::bytea, 'cloud-events')
	`, appID, acctID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("invalid delivery_format error = %v, want check violation 23514", err)
	}
}
