package state

import (
	"testing"

	"github.com/google/uuid"
)

func TestMemAppParkTransitionRecoversAndDedupesAcrossCycles(t *testing.T) {
	store, ctx, account, app := webhookFixture(t)
	hook := memSampleWebhook(account.ID, app.ID)
	hook.EventFilter = []string{string(AppWebhookEventAppParked)}
	hook, err := store.CreateAppWebhook(ctx, hook)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:park-outbox", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(StateRunning), 128, "node-1", "")
	if err != nil {
		t.Fatal(err)
	}

	transition, changed, err := store.BeginAppParkTransition(ctx, app.ID, AppActive)
	if err != nil || !changed || transition.ID == "" {
		t.Fatalf("begin park transition = %+v changed=%v err=%v", transition, changed, err)
	}
	if complete, err := store.CompleteDrainedAppParkTransition(ctx, transition.ID); err != nil || complete {
		t.Fatalf("completed before drain = %v, %v", complete, err)
	}
	if err := store.UpdateInstanceState(ctx, instance.ID, string(StateParked)); err != nil {
		t.Fatal(err)
	}
	// This is the dispatcher recovery path after the request process exits.
	if n, err := store.DrainDrainedAppParkTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("reconciled drained transition = %d, %v", n, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relayed parked event = %d, %v", n, err)
	}

	// An idempotent retry after the outbox row has been deleted reuses the
	// durable completed transition instead of creating a second event.
	retry, changed, err := store.BeginAppParkTransition(ctx, app.ID, AppEvictedCold)
	if err != nil || changed || retry.ID != transition.ID {
		t.Fatalf("retry transition = %+v changed=%v err=%v", retry, changed, err)
	}
	if complete, err := store.CompleteDrainedAppParkTransition(ctx, retry.ID); err != nil || complete {
		t.Fatalf("retry completion = %v, %v", complete, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 0 {
		t.Fatalf("duplicate outbox after retry = %d, %v", n, err)
	}
	got, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil || len(got) != 1 {
		t.Fatalf("deliveries after retry = %d, %v; want one", len(got), err)
	}

	if changed, err := store.CompareAndSetAppStatus(ctx, app.ID, AppEvictedCold, AppActive); err != nil || !changed {
		t.Fatalf("wake app for next park = %v, %v", changed, err)
	}
	second, changed, err := store.BeginAppParkTransition(ctx, app.ID, AppActive)
	if err != nil || !changed || second.ID == transition.ID {
		t.Fatalf("second park transition = %+v changed=%v err=%v", second, changed, err)
	}
	if n, err := store.DrainDrainedAppParkTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("second park completion = %d, %v", n, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("second parked event relay = %d, %v", n, err)
	}
	got, _, err = store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil || len(got) != 2 {
		t.Fatalf("deliveries after second park = %d, %v; want two", len(got), err)
	}
	if uuid.Validate(transition.ID) != nil || uuid.Validate(second.ID) != nil {
		t.Fatalf("park transition IDs are not UUIDs: %q %q", transition.ID, second.ID)
	}
}

func TestMemAppWebhookEventOutboxHealthTracksPendingOnly(t *testing.T) {
	store, ctx, account, app := webhookFixture(t)
	hook := memSampleWebhook(account.ID, app.ID)
	hook.EventFilter = []string{string(AppWebhookEventAppParked)}
	if _, err := store.CreateAppWebhook(ctx, hook); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.BeginAppParkTransition(ctx, app.ID, AppActive); err != nil || !changed {
		t.Fatalf("begin park transition changed=%v err=%v", changed, err)
	}
	if n, err := store.DrainDrainedAppParkTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("complete park transition = %d, %v", n, err)
	}
	health, err := store.AppWebhookEventOutboxHealth(ctx)
	if err != nil || health.PendingCount != 1 || health.OldestPendingAt == nil {
		t.Fatalf("pending event outbox health = %+v, %v", health, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relay pending event = %d, %v", n, err)
	}
	cleared, err := store.AppWebhookEventOutboxHealth(ctx)
	if err != nil || cleared.PendingCount != 0 || cleared.OldestPendingAt != nil {
		t.Fatalf("empty event outbox health = %+v, %v", cleared, err)
	}
}
