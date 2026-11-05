//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261024230731: the rehearsal webhook fence locks apps rows only on an
// insert, so a claim or completion does not wait for a transaction holding the
// app FOR UPDATE; an enrolled app's delivery still cannot become runnable, and
// the rollback restores the lock.
func TestRehearsalWebhookFenceInsertLockMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "webhook-fence@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	delivery := func(slug string) (appID, deliveryID string) {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
		if err != nil {
			t.Fatal(err)
		}
		var hookID string
		if err := pool.QueryRow(ctx, `INSERT INTO app_webhooks (app_id, account_id, target_url, secret_sealed)
			VALUES ($1, $2, 'https://example.com/fence', '\x00'::bytea) RETURNING id::text`, app.ID, acct.ID).Scan(&hookID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO app_webhook_deliveries (webhook_id, app_id, account_id, event, payload)
			VALUES ($1, $2, $3, 'deployment.succeeded', '{}'::jsonb) RETURNING id::text`, hookID, app.ID, acct.ID).Scan(&deliveryID); err != nil {
			t.Fatal(err)
		}
		return app.ID, deliveryID
	}
	ordinaryApp, ordinaryDelivery := delivery("fence-ordinary")
	enrolledApp, enrolledDelivery := delivery("fence-enrolled")
	if _, err := pool.Exec(ctx, `INSERT INTO scenario_test_action_policies (app_id, account_id, run_id, webhook_mode)
		VALUES ($1, $2, $3, 'capture')`, enrolledApp, acct.ID, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}

	raw, err := migrations.FS.ReadFile("20261024230705892_rehearsal_webhook_fence_insert_lock.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	// waits reports whether moving the ordinary delivery to status blocks on a
	// transaction that holds its app FOR UPDATE.
	waits := func(status string) bool {
		t.Helper()
		holder, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = holder.Rollback(ctx) }()
		if _, err := holder.Exec(ctx, `SELECT id FROM apps WHERE id = $1 FOR UPDATE`, ordinaryApp); err != nil {
			t.Fatal(err)
		}
		claim, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = claim.Rollback(ctx) }()
		if _, err := claim.Exec(ctx, `SET LOCAL lock_timeout = '300ms'`); err != nil {
			t.Fatal(err)
		}
		_, err = claim.Exec(ctx, `UPDATE app_webhook_deliveries SET status = $2 WHERE id = $1`, ordinaryDelivery, status)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		return false
	}
	// The enrolled app's delivery was suppressed on insert; a manual retry
	// must not make it runnable.
	suppressed := func() {
		t.Helper()
		var status string
		if err := pool.QueryRow(ctx, `UPDATE app_webhook_deliveries SET status = 'pending' WHERE id = $1 RETURNING status`, enrolledDelivery).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "dead" {
			t.Fatalf("enrolled delivery retried to %q, want dead", status)
		}
	}

	for _, status := range []string{"in_flight", "succeeded"} {
		if waits(status) {
			t.Fatalf("%s update waited on the app lock after the migration", status)
		}
	}
	suppressed()
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if !waits("in_flight") {
		t.Fatal("rollback did not restore the app lock on updates")
	}
	suppressed()
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if waits("in_flight") {
		t.Fatal("reapplied migration still locks the app on updates")
	}
	suppressed()
}
