package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// checkAPIConsumerSelfService runs the ADR-20261021183000 store contract: a
// completed sign-up, a customer's own plan change and closing each enqueue
// one app webhook event with the change, and a closed consumer can no
// longer change plans.
func checkAPIConsumerSelfService(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	plans := st.(state.APIConsumerPlanStore)
	pro, err := plans.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	team, err := plans.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "team"})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := st.CreateAppWebhook(ctx, state.AppWebhook{AppID: appID, AccountID: accountID, TargetURL: "https://owner.example/hook", SecretSealed: []byte("sealed"),
		Enabled: true, EventFilter: []string{"consumer.signed_up", "consumer.plan_changed", "consumer.closed"}})
	if err != nil {
		t.Fatal(err)
	}
	signups := st.(state.APIConsumerSignupStore)
	if _, err := signups.PutAPIConsumerSignupSettings(ctx, state.APIConsumerSignupSettings{AppID: appID, AccountID: accountID, Enabled: true,
		Provider: state.APIConsumerSignupProviderStripe, PlanIDs: []string{pro.ID, team.ID}, KeyScopes: []string{"write", "billing"},
		DisplayName: "Weather API", DailyLimit: 10, CustomerPlanChanges: true, CustomerClosing: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := signups.GetAPIConsumerSignupSettings(ctx, accountID, appID); err != nil || !got.CustomerPlanChanges || !got.CustomerClosing {
		t.Fatalf("settings = %+v, %v", got, err)
	}
	signup, err := signups.CreateAPIConsumerSignup(ctx, state.APIConsumerSignup{AccountID: accountID, AppID: appID, Email: "ada@example.com",
		Name: "Ada", PlanID: pro.ID, Provider: state.APIConsumerSignupProviderStripe, ClaimHash: bytes.Repeat([]byte{1}, 32),
		ExpiresAt: time.Now().Add(time.Hour)}, time.Now().UTC().Truncate(24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := signups.SetAPIConsumerSignupCheckout(ctx, accountID, signup.ID, "cus_selfservice", "cs_1"); err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	consumer, _, err := signups.CompleteAPIConsumerSignup(ctx, state.APIConsumerSignupCompletion{AccountID: accountID, SignupID: signup.ID,
		EffectiveFrom: minute, TrialDays: 7})
	if err != nil {
		t.Fatal(err)
	}

	self := st.(state.APIConsumerSelfServiceStore)
	change := state.APIConsumerPlanAssignment{AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, PlanID: team.ID, EffectiveFrom: minute.Add(time.Minute)}
	assignment, err := self.ChangeAPIConsumerPlanSelf(ctx, change, pro.ID)
	if err != nil || assignment.PlanID != team.ID || !assignment.EffectiveFrom.Equal(change.EffectiveFrom) {
		t.Fatalf("plan change = %+v, %v", assignment, err)
	}
	if _, err := self.ChangeAPIConsumerPlanSelf(ctx, change, pro.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second change for the same minute: %v, want conflict", err)
	}
	closed, changed, err := self.CloseAPIConsumerSelf(ctx, accountID, consumer.ID)
	if err != nil || !changed || closed.Active() || closed.RevokedAt == nil {
		t.Fatalf("close = %+v, %t, %v", closed, changed, err)
	}
	if _, changed, err := self.CloseAPIConsumerSelf(ctx, accountID, consumer.ID); err != nil || changed {
		t.Fatalf("closing again = %t, %v; want unchanged", changed, err)
	}
	change.EffectiveFrom = minute.Add(2 * time.Minute)
	if _, err := self.ChangeAPIConsumerPlanSelf(ctx, change, team.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("plan change after closing: %v, want conflict", err)
	}
	if _, _, err := self.CloseAPIConsumerSelf(ctx, accountID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("closing an unknown consumer: %v", err)
	}

	outbox := st.(state.AppWebhookEventOutboxStore)
	for event, source := range map[state.AppWebhookEvent]string{state.AppWebhookEventConsumerSignedUp: signup.ID,
		state.AppWebhookEventConsumerPlanChanged: assignment.ID, state.AppWebhookEventConsumerClosed: consumer.ID} {
		if _, err := outbox.RelayAppWebhookEventOutboxSource(ctx, event, source); err != nil {
			t.Fatalf("relay %s: %v", event, err)
		}
	}
	deliveries, _, err := st.ListAppWebhookDeliveries(ctx, appID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 3 {
		t.Fatalf("deliveries = %+v, %v; want sign-up, plan change and close", deliveries, err)
	}
	for _, d := range deliveries {
		switch d.Event {
		case state.AppWebhookEventConsumerSignedUp:
			var p api.APIConsumerSignedUpWebhookPayload
			if err := json.Unmarshal(d.Payload, &p); err != nil || p.SignupID != signup.ID || p.Email != "ada@example.com" ||
				p.ExternalRef != "cus_selfservice" || p.PlanID != pro.ID || p.TrialEndsAt == nil || !p.TrialEndsAt.Equal(minute.AddDate(0, 0, 7)) {
				t.Fatalf("signed_up payload = %s, %v", d.Payload, err)
			}
		case state.AppWebhookEventConsumerPlanChanged:
			var p api.APIConsumerPlanChangedWebhookPayload
			if err := json.Unmarshal(d.Payload, &p); err != nil || p.AssignmentID != assignment.ID || p.FromPlanID != pro.ID || p.ToPlanID != team.ID {
				t.Fatalf("plan_changed payload = %s, %v", d.Payload, err)
			}
		case state.AppWebhookEventConsumerClosed:
			var p api.APIConsumerClosedWebhookPayload
			if err := json.Unmarshal(d.Payload, &p); err != nil || p.ConsumerID != consumer.ID || p.ClosedAt.IsZero() {
				t.Fatalf("closed payload = %s, %v", d.Payload, err)
			}
		default:
			t.Fatalf("unexpected delivery %s", d.Event)
		}
	}
}

// adr: 20261021183000
func TestMemStoreAPIConsumerSelfService(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerSelfService(t, ctx, store, accountID, appID)
}

// adr: 20261021183000
func TestPgStoreAPIConsumerSelfService(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkAPIConsumerSelfService(t, ctx, store, accountID, appID)
}

// adr: 20261021183000 — the outbox accepts every consumer event, including
// consumer.usage_threshold, which its event check used to refuse.
func TestPgAppWebhookOutboxAcceptsConsumerEvents(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	for _, event := range []state.AppWebhookEvent{state.AppWebhookEventConsumerUsageThreshold, state.AppWebhookEventConsumerSignedUp,
		state.AppWebhookEventConsumerPlanChanged, state.AppWebhookEventConsumerClosed} {
		if _, err := pool.Exec(ctx, `insert into app_webhook_event_outbox (account_id, app_id, event, source_id, payload, recipient_webhook_ids)
			values ($1::uuid, $2::uuid, $3, $4::uuid, '{}'::jsonb, array[$5::uuid])`, accountID, appID, string(event), uuid.NewString(), uuid.NewString()); err != nil {
			t.Errorf("outbox refused %s: %v", event, err)
		}
	}
}
