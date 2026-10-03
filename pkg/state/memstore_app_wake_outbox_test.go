package state

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestMemAppWakeTransitionRecoversReadyInstanceAndDedupes(t *testing.T) {
	store, ctx, account, app := webhookFixture(t)
	parked := AppEvictedCold
	if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	hook := memSampleWebhook(account.ID, app.ID)
	hook.EventFilter = []string{string(AppWebhookEventAppWoken)}
	hook, err := store.CreateAppWebhook(ctx, hook)
	if err != nil {
		t.Fatal(err)
	}
	transition, changed, err := store.BeginAppWakeTransition(ctx, app.ID)
	if err != nil || !changed || transition.ID == "" {
		t.Fatalf("begin wake transition = %+v changed=%v err=%v", transition, changed, err)
	}
	duplicate, changed, err := store.BeginAppWakeTransition(ctx, app.ID)
	if err != nil || changed || duplicate.ID != transition.ID {
		t.Fatalf("duplicate wake transition = %+v changed=%v err=%v", duplicate, changed, err)
	}
	if complete, err := store.CompleteReadyAppWakeTransition(ctx, transition.ID, uuid.NewString(), uuid.NewString()); err != nil || complete {
		t.Fatalf("completed transition without ready instance = %v, %v", complete, err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:wake-outbox", Status: DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	wakeID := "0198f89a-0000-7000-8000-000000000011"
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(StateRunning), 128, "node-1", wakeID)
	if err != nil {
		t.Fatal(err)
	}

	// A fresh dispatcher/store view recovers the transition after the wake
	// request path has disappeared.
	if n, err := store.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 1 {
		t.Fatalf("recovered ready wake transitions = %d, %v", n, err)
	}
	if n, err := store.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 0 {
		t.Fatalf("duplicate wake recovery = %d, %v", n, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 16); err != nil || n != 1 {
		t.Fatalf("relayed app.woken event = %d, %v", n, err)
	}
	if complete, err := store.CompleteReadyAppWakeTransition(ctx, transition.ID, instance.ID, wakeID); err != nil || complete {
		t.Fatalf("completed transition retry = %v, %v", complete, err)
	}
	if rolledBack, err := store.AbortAppWakeTransition(ctx, transition.ID); err != nil || rolledBack {
		t.Fatalf("abort completed transition = %v, %v", rolledBack, err)
	}
	if current, err := store.AppByID(ctx, app.ID); err != nil || current.Status != AppActive {
		t.Fatalf("app after completed wake abort = %q, %v; want active", current.Status, err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != AppWebhookEventAppWoken {
		t.Fatalf("deliveries = %+v, %v; want one app.woken", deliveries, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["instance_id"] != instance.ID || payload["wake_id"] != wakeID || payload["app_id"] != app.ID {
		t.Fatalf("app.woken payload = %+v", payload)
	}
}

func TestMemAppWakeTransitionFailureAndParkSupersede(t *testing.T) {
	t.Run("failed boot", func(t *testing.T) {
		store, ctx, _, app := webhookFixture(t)
		parked := AppEvictedCold
		if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{Status: &parked}); err != nil {
			t.Fatal(err)
		}
		transition, changed, err := store.BeginAppWakeTransition(ctx, app.ID)
		if err != nil || !changed {
			t.Fatalf("begin wake transition = %+v changed=%v err=%v", transition, changed, err)
		}
		if rolledBack, err := store.AbortAppWakeTransition(ctx, transition.ID); err != nil || !rolledBack {
			t.Fatalf("abort wake transition = %v, %v", rolledBack, err)
		}
		current, err := store.AppByID(ctx, app.ID)
		if err != nil || current.Status != AppEvictedCold {
			t.Fatalf("app after failed wake = %q, %v; want evicted_cold", current.Status, err)
		}
		if n, err := store.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 0 {
			t.Fatalf("failed wake recovery = %d, %v", n, err)
		}
	})

	t.Run("park wins", func(t *testing.T) {
		store, ctx, _, app := webhookFixture(t)
		parked := AppEvictedCold
		if _, err := store.UpdateApp(ctx, app.ID, UpdateAppParams{Status: &parked}); err != nil {
			t.Fatal(err)
		}
		transition, changed, err := store.BeginAppWakeTransition(ctx, app.ID)
		if err != nil || !changed {
			t.Fatalf("begin wake transition = %+v changed=%v err=%v", transition, changed, err)
		}
		if _, changed, err := store.BeginAppParkTransition(ctx, app.ID, AppActive); err != nil || !changed {
			t.Fatalf("begin park transition changed=%v err=%v", changed, err)
		}
		if n, err := store.DrainReadyAppWakeTransitions(ctx, 16); err != nil || n != 0 {
			t.Fatalf("parked wake recovery = %d, %v", n, err)
		}
		if complete, err := store.CompleteReadyAppWakeTransition(ctx, transition.ID, uuid.NewString(), uuid.NewString()); err != nil || complete {
			t.Fatalf("parked wake completion = %v, %v", complete, err)
		}
	})
}

func TestMemAppLifecycleTransitionHealthTracksPendingOnly(t *testing.T) {
	store, ctx, _, app := webhookFixture(t)
	if _, changed, err := store.BeginAppParkTransition(ctx, app.ID, AppActive); err != nil || !changed {
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
	wake, changed, err := store.BeginAppWakeTransition(ctx, app.ID)
	if err != nil || !changed {
		t.Fatalf("begin wake transition = %+v changed=%v err=%v", wake, changed, err)
	}
	waking, err := store.AppLifecycleTransitionHealth(ctx)
	if err != nil || waking.WakePendingCount != 1 || waking.WakeOldestPendingAt == nil || waking.ParkPendingCount != 0 {
		t.Fatalf("pending wake health = %+v, %v", waking, err)
	}
	if rolledBack, err := store.AbortAppWakeTransition(ctx, wake.ID); err != nil || !rolledBack {
		t.Fatalf("abort wake transition = %v, %v", rolledBack, err)
	}
	terminal, err := store.AppLifecycleTransitionHealth(ctx)
	if err != nil || terminal.WakePendingCount != 0 || terminal.WakeOldestPendingAt != nil {
		t.Fatalf("terminal wake health = %+v, %v", terminal, err)
	}
}
