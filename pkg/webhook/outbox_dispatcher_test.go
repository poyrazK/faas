package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDispatcherCycleRelaysCommittedWebhookEventWithoutDeliverySlots(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{
		AccountID: accountID, AppID: appID, TargetURL: "https://example.com/statements",
		EventFilter: []string{string(state.AppWebhookEventUsageStatementFinalized)}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	statement, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Revision: 1, AsOf: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumerID, statement.ID); err != nil || !changed {
		t.Fatalf("finalize changed=%v err=%v", changed, err)
	}
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.Cap = 0 // exercise relay even when dispatch cannot reserve worker slots
	d.cycle(ctx)
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("relayed deliveries = %+v, %v", deliveries, err)
	}
	if n, err := store.DrainAppWebhookEventOutbox(ctx, 10); err != nil || n != 0 {
		t.Fatalf("outbox after cycle = %d, %v", n, err)
	}
}

func TestDispatcherCycleCompletesDrainedAppParkTransition(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "park-outbox-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "park-outbox-" + uuid.NewString(), RAMMB: 512, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{
		AccountID: account.ID, AppID: app.ID, TargetURL: "https://example.com/parked",
		EventFilter: []string{string(state.AppWebhookEventAppParked)}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.BeginAppParkTransition(ctx, app.ID, state.AppActive); err != nil || !changed {
		t.Fatalf("begin park transition changed=%v err=%v", changed, err)
	}
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.Cap = 0
	d.cycle(ctx)
	d.cycle(ctx)
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventAppParked {
		t.Fatalf("recovered parked deliveries = %+v, %v", deliveries, err)
	}
}

func TestDispatcherCycleCompletesReadyAppWakeTransition(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "wake-outbox-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "wake-outbox-" + uuid.NewString(), RAMMB: 512, Status: state.AppEvictedCold,
	})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{
		AccountID: account.ID, AppID: app.ID, TargetURL: "https://example.com/woken",
		EventFilter: []string{string(state.AppWebhookEventAppWoken)}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	transition, changed, err := store.BeginAppWakeTransition(ctx, app.ID)
	if err != nil || !changed {
		t.Fatalf("begin wake transition = %+v changed=%v err=%v", transition, changed, err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:ready-wake", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "node-1", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.Cap = 0 // prove recovery and relay run without delivery claim slots
	d.cycle(ctx)
	d.cycle(ctx)
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventAppWoken {
		t.Fatalf("recovered woken deliveries = %+v, %v", deliveries, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["instance_id"] != instance.ID {
		t.Fatalf("recovered app.woken payload = %+v", payload)
	}
}
