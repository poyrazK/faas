//go:build !no_pg

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_DeploymentLifecycleWebhooks(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, accountID)
	otherAccountID := seedAccount(t, ctx, pool)

	insertHook := func(ownerID, filter string, enabled bool) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `
			insert into app_webhooks (app_id, account_id, target_url, secret_sealed, event_filter, enabled)
			values ($1, $2, 'https://example.com/' || gen_random_uuid()::text, $3, $4::text[], $5)
			returning id::text`, appID, ownerID, []byte("sealed"), filter, enabled).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	liveHook := insertHook(accountID, "{deployment.live}", true)
	allHook := insertHook(accountID, "{}", true)
	disabledHook := insertHook(accountID, "{}", false)
	foreignHook := insertHook(otherAccountID, "{}", true)

	liveID := seedDeployment(t, ctx, pool, appID, "pending")
	if _, err := pool.Exec(ctx, `update deployments set status='live' where id=$1`, liveID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status='live' where id=$1`, liveID); err != nil {
		t.Fatal(err)
	}

	failedID := seedDeployment(t, ctx, pool, appID, "pending")
	if _, err := pool.Exec(ctx, `
		update deployments set status='failed', error_code='build_timeout',
			error_hint='Try a smaller build', error_fix='Reduce dependencies'
		where id=$1`, failedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status='failed' where id=$1`, failedID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		hookID string
		want   int
	}{
		{liveHook, 1}, {allHook, 2}, {disabledHook, 0}, {foreignHook, 0},
	} {
		var count int
		if err := pool.QueryRow(ctx, `select count(*) from app_webhook_deliveries where webhook_id=$1`, tc.hookID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != tc.want {
			t.Errorf("hook %s deliveries = %d, want %d", tc.hookID, count, tc.want)
		}
	}

	var raw []byte
	if err := pool.QueryRow(ctx, `
		select payload from app_webhook_deliveries
		where webhook_id=$1 and event='deployment.failed'`, allHook).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["deployment_id"] != failedID || payload["status"] != "failed" || payload["error_hint"] != "Try a smaller build" {
		t.Errorf("failed payload = %v", payload)
	}
	if _, leaked := payload["error"]; leaked {
		t.Errorf("internal error text leaked in payload: %v", payload)
	}

	rolledBackID := seedDeployment(t, ctx, pool, appID, "pending")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update deployments set status='failed' where id=$1`, rolledBackID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `
		select count(*) from app_webhook_deliveries
		where payload->>'deployment_id'=$1`, rolledBackID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("rolled-back transition created %d deliveries", count)
	}
}
