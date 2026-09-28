//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppWakeTransitionRecoveryAndDedupes(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(ctx, appID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	hookInput := pgSampleWebhook(accountID, appID)
	hookInput.EventFilter = []string{string(state.AppWebhookEventAppWoken)}
	hook, err := store.CreateAppWebhook(ctx, hookInput)
	if err != nil {
		t.Fatal(err)
	}
	transition, changed, err := store.BeginAppWakeTransition(ctx, appID)
	if err != nil || !changed || transition.ID == "" {
		t.Fatalf("begin wake transition = %+v changed=%v err=%v", transition, changed, err)
	}
	duplicate, changed, err := store.BeginAppWakeTransition(ctx, appID)
	if err != nil || changed || duplicate.ID != transition.ID {
		t.Fatalf("duplicate wake transition = %+v changed=%v err=%v", duplicate, changed, err)
	}
	restarted := state.NewPgStore(pool)
	if n, err := restarted.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 0 {
		t.Fatalf("recovered transition before instance readiness = %d, %v", n, err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:wake-outbox", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	var nodeID string
	if err := pool.QueryRow(ctx, `select id from compute_nodes where name = 'default-local' limit 1`).Scan(&nodeID); err != nil {
		t.Fatalf("lookup default-local node: %v", err)
	}
	wakeID := uuid.NewString()
	instance, err := store.CreateInstance(ctx, appID, dep.ID, string(state.StateRunning), 128, nodeID, wakeID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := restarted.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("recovered ready transition = %d, %v", n, err)
	}
	if n, err := restarted.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 0 {
		t.Fatalf("duplicate recovery = %d, %v", n, err)
	}
	if n, err := restarted.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relayed app.woken event = %d, %v", n, err)
	}
	if complete, err := store.CompleteReadyAppWakeTransition(ctx, transition.ID, instance.ID, wakeID); err != nil || complete {
		t.Fatalf("completed transition retry = %v, %v", complete, err)
	}
	if rolledBack, err := store.AbortAppWakeTransition(ctx, transition.ID); err != nil || rolledBack {
		t.Fatalf("abort completed transition = %v, %v", rolledBack, err)
	}
	if app, err := store.AppByID(ctx, appID); err != nil || app.Status != state.AppActive {
		t.Fatalf("app after completed wake abort = %q, %v; want active", app.Status, err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventAppWoken {
		t.Fatalf("deliveries = %+v, %v; want one app.woken", deliveries, err)
	}
}

func TestPgAppWakeTransitionFailureAndParkSupersede(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	_, appID := seedConsumerKeyAccountApp(t, ctx, store)
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(ctx, appID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	failed, changed, err := store.BeginAppWakeTransition(ctx, appID)
	if err != nil || !changed {
		t.Fatalf("begin failed wake transition = %+v changed=%v err=%v", failed, changed, err)
	}
	if rolledBack, err := store.AbortAppWakeTransition(ctx, failed.ID); err != nil || !rolledBack {
		t.Fatalf("abort failed wake transition = %v, %v", rolledBack, err)
	}
	if app, err := store.AppByID(ctx, appID); err != nil || app.Status != state.AppEvictedCold {
		t.Fatalf("app after failed wake = %q, %v; want evicted_cold", app.Status, err)
	}
	wake, changed, err := store.BeginAppWakeTransition(ctx, appID)
	if err != nil || !changed {
		t.Fatalf("begin wake before park = %+v changed=%v err=%v", wake, changed, err)
	}
	if _, changed, err := store.BeginAppParkTransition(ctx, appID, state.AppActive); err != nil || !changed {
		t.Fatalf("park superseding wake changed=%v err=%v", changed, err)
	}
	if n, err := store.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 0 {
		t.Fatalf("parked wake recovery = %d, %v", n, err)
	}
	if app, err := store.AppByID(ctx, appID); err != nil || app.Status != state.AppEvictedCold {
		t.Fatalf("app after park wins = %q, %v; want evicted_cold", app.Status, err)
	}
}
