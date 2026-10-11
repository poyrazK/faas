package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppParkTransitionRecoveryAndRetry(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	hookInput := pgSampleWebhook(accountID, appID)
	hookInput.EventFilter = []string{string(state.AppWebhookEventAppParked)}
	hook, err := store.CreateAppWebhook(ctx, hookInput)
	if err != nil {
		t.Fatal(err)
	}

	transition, changed, err := store.BeginAppParkTransition(ctx, appID, state.AppActive)
	if err != nil || !changed || transition.ID == "" {
		t.Fatalf("begin park transition = %+v changed=%v err=%v", transition, changed, err)
	}
	if app, err := store.AppByID(ctx, appID); err != nil || app.Status != state.AppEvictedCold {
		t.Fatalf("park status = %q, %v", app.Status, err)
	}

	// A new store instance completes the durable transition after the API
	// process exits between the drain check and its inline completion call.
	restarted := state.NewPgStore(pool)
	if n, err := restarted.DrainDrainedAppParkTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("recovered park transition = %d, %v", n, err)
	}
	if n, err := restarted.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relayed parked event = %d, %v", n, err)
	}
	if complete, err := store.CompleteDrainedAppParkTransition(ctx, transition.ID); err != nil || complete {
		t.Fatalf("completed transition retry = %v, %v", complete, err)
	}
	retry, changed, err := store.BeginAppParkTransition(ctx, appID, state.AppEvictedCold)
	if err != nil || changed || retry.ID != transition.ID {
		t.Fatalf("idempotent park retry = %+v changed=%v err=%v", retry, changed, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 0 {
		t.Fatalf("duplicate outbox after retry = %d, %v", n, err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries after retry = %d, %v; want one", len(deliveries), err)
	}

	if changed, err := store.CompareAndSetAppStatus(ctx, appID, state.AppEvictedCold, state.AppActive); err != nil || !changed {
		t.Fatalf("wake app for next park = %v, %v", changed, err)
	}
	second, changed, err := store.BeginAppParkTransition(ctx, appID, state.AppActive)
	if err != nil || !changed || second.ID == transition.ID {
		t.Fatalf("second park transition = %+v changed=%v err=%v", second, changed, err)
	}
	if n, err := store.DrainDrainedAppParkTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("second park completion = %d, %v", n, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("second parked event relay = %d, %v", n, err)
	}
	deliveries, _, err = store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("deliveries after second park = %d, %v; want two", len(deliveries), err)
	}
}

func TestPgAppLifecycleTransitionHealthTracksPendingOnly(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID := seedConsumerKeyAccountApp(t, ctx, store)
	if _, changed, err := store.BeginAppParkTransition(ctx, appID, state.AppActive); err != nil || !changed {
		t.Fatalf("begin park transition changed=%v err=%v", changed, err)
	}
	health, err := store.AppLifecycleTransitionHealth(ctx)
	if err != nil || health.ParkPendingCount != 1 || health.ParkOldestPendingAt == nil || health.WakePendingCount != 0 {
		t.Fatalf("pending park health = %+v, %v", health, err)
	}
	if n, err := store.DrainDrainedAppParkTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("complete park transition = %d, %v", n, err)
	}
	parked, err := store.AppLifecycleTransitionHealth(ctx)
	if err != nil || parked.ParkPendingCount != 0 || parked.ParkOldestPendingAt != nil {
		t.Fatalf("completed park health = %+v, %v", parked, err)
	}
	wake, changed, err := store.BeginAppWakeTransition(ctx, appID)
	if err != nil || !changed {
		t.Fatalf("begin wake transition = %+v changed=%v err=%v", wake, changed, err)
	}
	waking, err := store.AppLifecycleTransitionHealth(ctx)
	if err != nil || waking.WakePendingCount != 1 || waking.WakeOldestPendingAt == nil || waking.ParkPendingCount != 0 {
		t.Fatalf("pending wake health = %+v, %v", waking, err)
	}
	if aborted, err := store.AbortAppWakeTransition(ctx, wake.ID); err != nil || !aborted {
		t.Fatalf("abort wake transition = %v, %v", aborted, err)
	}
	terminal, err := store.AppLifecycleTransitionHealth(ctx)
	if err != nil || terminal.WakePendingCount != 0 || terminal.WakeOldestPendingAt != nil {
		t.Fatalf("terminal wake health = %+v, %v", terminal, err)
	}
}

func TestPgAppParkRejectsDeploymentCommittedDuringRetirement(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, appID, firstID := seedLiveDeploy(t, store, ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Deployment creation and conditional parking serialize on this app row.
	if _, err := tx.Exec(ctx, `select id from apps where id=$1::uuid for update`, appID); err != nil {
		t.Fatal(err)
	}
	secondID := uuid.NewString()
	if _, err := tx.Exec(ctx, `insert into deployments (id, app_id, image_digest, status, created_at) values ($1::uuid,$2::uuid,'sha256:new-generation','pending',clock_timestamp())`, secondID, appID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := store.BeginAppParkTransitionIfDeployment(ctx, appID, state.AppActive, firstID)
		result <- err
	}()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale park: %v", err)
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil || app.Status != state.AppActive {
		t.Fatalf("new generation was parked: %s %v", app.Status, err)
	}
	if _, changed, err := store.BeginAppParkTransitionIfDeployment(ctx, appID, state.AppActive, secondID); err != nil || !changed {
		t.Fatalf("current park: changed=%v err=%v", changed, err)
	}
	if _, _, err := store.BeginAppParkTransitionIfDeployment(ctx, appID, state.AppEvictedCold, firstID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale retry: %v", err)
	}
}
