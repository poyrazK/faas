package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEmit_FansOutMatchingSubscriptions(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "webhook-events@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "webhook-events", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.CreateAppWebhook(ctx, state.AppWebhook{AppID: app.ID, AccountID: acct.ID, TargetURL: "https://all.example/hook", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := store.CreateAppWebhook(ctx, state.AppWebhook{AppID: app.ID, AccountID: acct.ID, TargetURL: "https://filtered.example/hook", EventFilter: []string{"error.new"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := Emit(ctx, store, app.ID, state.AppWebhookEventDeploymentFailed, map[string]any{"deployment_id": "dep-1"}); err != nil {
		t.Fatal(err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, all.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].WebhookID != all.ID {
		t.Fatalf("deliveries = %+v, want one delivery for %s (filtered=%s)", deliveries, all.ID, filtered.ID)
	}
	var got map[string]any
	if err := json.Unmarshal(deliveries[0].Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["deployment_id"] != "dep-1" {
		t.Fatalf("payload = %v, want deployment_id", got)
	}
}

func TestEmit_RejectsUnknownEvent(t *testing.T) {
	err := Emit(context.Background(), state.NewMemStore(), "app", state.AppWebhookEvent("not.valid"), nil)
	if err == nil {
		t.Fatal("Emit accepted an unknown event")
	}
}
