//go:build !no_pg

// ADR-224: account release deliveries are selected in the source transition.
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_AccountReleaseWebhookFanout(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner := seedAccount(t, ctx, pool)
	other := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, owner)

	insertHook := func(accountID string, appID any, scope string, filter []string, enabled bool) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `
			insert into app_webhooks
			    (app_id, account_id, scope, target_url, secret_sealed, event_filter, enabled)
			values ($1, $2, $3, 'https://example.com/' || gen_random_uuid()::text,
			        $4, $5::text[], $6)
			returning id::text`, appID, accountID, scope, []byte("sealed"), filter, enabled).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	allEvents := []string{
		"deployment.live", "deployment.failed", "rollout.completed", "rollout.aborted",
	}
	accountHook := insertHook(owner, nil, "account", allEvents, true)
	liveOnlyHook := insertHook(owner, nil, "account", []string{"deployment.live"}, true)
	disabledHook := insertHook(owner, nil, "account", allEvents, false)
	foreignHook := insertHook(other, nil, "account", allEvents, true)
	appHook := insertHook(owner, appID, "app", []string{}, true)

	liveID := seedDeployment(t, ctx, pool, appID, "pending")
	for range 2 {
		if _, err := pool.Exec(ctx, `update deployments set status='live' where id=$1`, liveID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update deployments set status='superseded' where id=$1`, liveID); err != nil {
		t.Fatal(err)
	}
	failedID := seedDeployment(t, ctx, pool, appID, "pending")
	for range 2 {
		if _, err := pool.Exec(ctx, `update deployments set status='failed' where id=$1`, failedID); err != nil {
			t.Fatal(err)
		}
	}
	completedID := seedDeployment(t, ctx, pool, appID, "live")
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='rolling_out' where id=$1`, completedID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := pool.Exec(ctx, `update deployments set rollout_state='complete' where id=$1`, completedID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update deployments set status='superseded' where id=$1`, completedID); err != nil {
		t.Fatal(err)
	}
	abortedID := seedDeployment(t, ctx, pool, appID, "live")
	if _, err := pool.Exec(ctx, `update deployments set rollout_state='rolling_out' where id=$1`, abortedID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := pool.Exec(ctx, `update deployments set rollout_state='aborted' where id=$1`, abortedID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update deployments set status='superseded' where id=$1`, abortedID); err != nil {
		t.Fatal(err)
	}

	assertCount := func(hookID string, want int) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `select count(*) from app_webhook_deliveries where webhook_id=$1`, hookID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("hook %s deliveries = %d, want %d", hookID, count, want)
		}
	}
	assertCount(accountHook, 4)
	assertCount(liveOnlyHook, 1)
	assertCount(disabledHook, 0)
	assertCount(foreignHook, 0)
	assertCount(appHook, 4)

	var distinctIDs, deliveryCount int
	if err := pool.QueryRow(ctx, `
		select count(distinct id), count(*) from app_webhook_deliveries
		where app_id=$1 and event='deployment.live' and payload->>'deployment_id'=$2
		  and webhook_id in ($3, $4)`, appID, liveID, accountHook, appHook,
	).Scan(&distinctIDs, &deliveryCount); err != nil {
		t.Fatal(err)
	}
	if distinctIDs != 2 || deliveryCount != 2 {
		t.Errorf("app and account hooks got %d distinct IDs across %d deliveries, want 2/2", distinctIDs, deliveryCount)
	}
	var badSourceCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from app_webhook_deliveries
		where webhook_id=$1 and (app_id<>$2 or account_id<>$3 or payload->>'app_id'<>$2::text)`,
		accountHook, appID, owner,
	).Scan(&badSourceCount); err != nil {
		t.Fatal(err)
	}
	if badSourceCount != 0 {
		t.Errorf("account receiver has %d misattributed source deliveries", badSourceCount)
	}

	// A foreign account's app emits only to its own account receiver.
	foreignAppID := seedApp(t, ctx, pool, other)
	foreignLiveID := seedDeployment(t, ctx, pool, foreignAppID, "pending")
	if _, err := pool.Exec(ctx, `update deployments set status='live' where id=$1`, foreignLiveID); err != nil {
		t.Fatal(err)
	}
	assertCount(accountHook, 4)
	assertCount(foreignHook, 1)

	// New apps are covered without copying the subscription to them.
	newAppID := seedApp(t, ctx, pool, owner)
	newLiveID := seedDeployment(t, ctx, pool, newAppID, "pending")
	if _, err := pool.Exec(ctx, `update deployments set status='live' where id=$1`, newLiveID); err != nil {
		t.Fatal(err)
	}
	assertCount(accountHook, 5)
	assertCount(appHook, 4)
	var newAppDeliveryCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from app_webhook_deliveries
		where webhook_id=$1 and app_id=$2 and account_id=$3`,
		accountHook, newAppID, owner,
	).Scan(&newAppDeliveryCount); err != nil {
		t.Fatal(err)
	}
	if newAppDeliveryCount != 1 {
		t.Errorf("new app created %d account deliveries, want 1", newAppDeliveryCount)
	}

	// The enqueue remains atomic with the source transition.
	rolledBackID := seedDeployment(t, ctx, pool, appID, "pending")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update deployments set status='live' where id=$1`, rolledBackID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var rolledBackCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from app_webhook_deliveries
		where webhook_id=$1 and payload->>'deployment_id'=$2`,
		accountHook, rolledBackID,
	).Scan(&rolledBackCount); err != nil {
		t.Fatal(err)
	}
	if rolledBackCount != 0 {
		t.Errorf("rolled-back transition left %d account deliveries", rolledBackCount)
	}
}
