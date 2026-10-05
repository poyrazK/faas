package webhook

// adr: 595

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

// ADR-595: health changes require explicit opt-in even for wildcard hooks.
func TestEmit_HealthRequiresExplicitSubscription(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "health-opt-in@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "health-opt-in", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		filter []string
		want   int
	}{
		{"wildcard", nil, 0},
		{"explicit", []string{"app.health.changed"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			hook, err := store.CreateAppWebhook(t.Context(), state.AppWebhook{
				AppID: app.ID, AccountID: account.ID, TargetURL: "https://" + test.name + ".example/hook", EventFilter: test.filter, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := Emit(t.Context(), store, app.ID, state.AppWebhookEventAppHealthChanged, api.AppHealthChangedWebhookPayload{Status: "unhealthy"}); err != nil {
				t.Fatal(err)
			}
			deliveries, _, err := store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
			if err != nil || len(deliveries) != test.want {
				t.Fatalf("deliveries = %d, want %d: %v", len(deliveries), test.want, err)
			}
		})
	}
}

func TestRegressionLifecycleEventsAreSubscribable(t *testing.T) {
	for _, event := range []state.AppWebhookEvent{
		state.AppWebhookEventDebugRegressionDetected,
		state.AppWebhookEventDebugRegressionResolved,
	} {
		if !state.ValidAppWebhookEvent(event) {
			t.Errorf("state.ValidAppWebhookEvent(%q) = false", event)
		}
		found := false
		for _, allowed := range api.AllowedAppWebhookEvents {
			if allowed == string(event) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("API does not allow subscriptions to %q", event)
		}
	}
}

func TestEmit_UsageStatementFinalizedPayload(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "statement-webhook@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "statement-webhook", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{
		AppID: app.ID, AccountID: acct.ID, TargetURL: "https://billing.example/hook",
		EventFilter: []string{string(state.AppWebhookEventUsageStatementFinalized)}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := api.APIConsumerUsageStatementFinalizedWebhookPayload{
		AppID: app.ID, ConsumerID: "consumer-1", StatementID: "statement-1",
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC),
		Currency:    "EUR", BillableUnits: 3, AmountMillicents: 75, Priced: true,
		Buckets: []api.APIConsumerUsageStatementBucketResponse{{BillableUnits: 3, AmountMillicents: 75}},
		AsOf:    "2026-09-01T01:00:00Z", FinalizedAt: time.Date(2026, 9, 1, 1, 1, 0, 0, time.UTC),
	}
	if err := Emit(ctx, store, app.ID, state.AppWebhookEventUsageStatementFinalized, payload); err != nil {
		t.Fatal(err)
	}
	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventUsageStatementFinalized {
		t.Fatalf("deliveries = %+v, want one finalized statement delivery", deliveries)
	}
	var got api.APIConsumerUsageStatementFinalizedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.StatementID != payload.StatementID || got.AmountMillicents != payload.AmountMillicents || len(got.Buckets) != 1 {
		t.Fatalf("payload = %+v, want statement %q amount %d", got, payload.StatementID, payload.AmountMillicents)
	}
}
