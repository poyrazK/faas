//go:build !no_pg

// ADR-076: rollout-state transitions must enqueue replay-safe deliveries.

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_RolloutOutcomeWebhooks(t *testing.T) {
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
	completedHook := insertHook(accountID, "{rollout.completed}", true)
	allHook := insertHook(accountID, "{}", true)
	disabledHook := insertHook(accountID, "{}", false)
	foreignHook := insertHook(otherAccountID, "{}", true)

	completedID := seedDeployment(t, ctx, pool, appID, "live")
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='rolling_out' where id=$1`, completedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='complete', traffic_percent=100, rollout_completed_at=now() where id=$1`, completedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='complete' where id=$1`, completedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status='superseded' where id=$1`, completedID); err != nil {
		t.Fatal(err)
	}

	abortedID := seedDeployment(t, ctx, pool, appID, "live")
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='rolling_out' where id=$1`, abortedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='aborted', rollout_aborted_reason='manual stop', rollout_aborted_at=now() where id=$1`, abortedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='aborted' where id=$1`, abortedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status='superseded' where id=$1`, abortedID); err != nil {
		t.Fatal(err)
	}

	failedBeforeLiveID := seedDeployment(t, ctx, pool, appID, "pending")
	if _, err := pool.Exec(ctx, `update deployments set status='failed', rollout_state='aborted' where id=$1`, failedBeforeLiveID); err != nil {
		t.Fatal(err)
	}
	failedAfterLiveID := seedDeployment(t, ctx, pool, appID, "live")
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='rolling_out' where id=$1`, failedAfterLiveID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status='failed', rollout_state='aborted', rollout_aborted_reason='internal failure detail' where id=$1`, failedAfterLiveID); err != nil {
		t.Fatal(err)
	}
	var failedRolloutCount int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_deliveries where event like 'rollout.%' and payload->>'deployment_id'=$1`, failedAfterLiveID).Scan(&failedRolloutCount); err != nil {
		t.Fatal(err)
	}
	if failedRolloutCount != 0 {
		t.Errorf("runtime failure created %d rollout events; use deployment.failed", failedRolloutCount)
	}

	for _, tc := range []struct {
		hookID string
		want   int
	}{
		{completedHook, 1}, {allHook, 2}, {disabledHook, 0}, {foreignHook, 0},
	} {
		var count int
		if err := pool.QueryRow(ctx, `select count(*) from app_webhook_deliveries where webhook_id=$1 and event like 'rollout.%'`, tc.hookID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != tc.want {
			t.Errorf("hook %s rollout deliveries = %d, want %d", tc.hookID, count, tc.want)
		}
	}

	for _, tc := range []struct {
		event, id, state string
	}{
		{"rollout.completed", completedID, "complete"},
		{"rollout.aborted", abortedID, "aborted"},
	} {
		var raw []byte
		if err := pool.QueryRow(ctx, `select payload from app_webhook_deliveries where webhook_id=$1 and event=$2`, allHook, tc.event).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["app_id"] != appID || payload["deployment_id"] != tc.id || payload["rollout_state"] != tc.state {
			t.Errorf("%s payload = %v", tc.event, payload)
		}
		if tc.event == "rollout.completed" && (payload["traffic_percent"] != float64(100) || payload["completed_at"] == nil) {
			t.Errorf("completed payload = %v", payload)
		}
		if tc.event == "rollout.aborted" && (payload["reason"] != "manual stop" || payload["aborted_at"] == nil) {
			t.Errorf("aborted payload = %v", payload)
		}
	}

	rolledBackID := seedDeployment(t, ctx, pool, appID, "live")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update deployments set rollout_state='aborted' where id=$1`, rolledBackID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhook_deliveries where payload->>'deployment_id'=$1`, rolledBackID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("rolled-back transition created %d deliveries", count)
	}
}
