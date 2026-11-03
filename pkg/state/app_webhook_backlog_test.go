package state_test

// adr: 20261101093517 — outbox caps, deferred accounts, per-account in-flight
// cap and secret rotation overlap.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func backlogWebhookFixture(t *testing.T) (*state.MemStore, context.Context, state.Account, state.App) {
	t.Helper()
	ctx := context.Background()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "backlog-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "backlog-" + uuid.NewString()[:8], RAMMB: 512, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, acct, app
}

func backlogWebhook(t *testing.T, m *state.MemStore, ctx context.Context, acct state.Account, app state.App) state.AppWebhook {
	t.Helper()
	hook, err := m.CreateAppWebhook(ctx, state.AppWebhook{
		AccountID: acct.ID, AppID: app.ID, TargetURL: "https://example.com/hook-" + uuid.NewString(),
		SecretSealed: []byte("sealed-old"), EventFilter: []string{"cron.fired"},
		RetryPolicy: state.AppWebhookRetryDefault, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func backlogDelivery(hook state.AppWebhook, payload string, maxPending int, maxBytes int64) state.AppWebhookDelivery {
	return state.AppWebhookDelivery{
		WebhookID: hook.ID, AppID: hook.AppID, AccountID: hook.AccountID, Event: "cron.fired",
		Payload: []byte(payload), Status: state.AppWebhookDeliveryPending,
		NextAttemptAt: time.Now().Add(-time.Minute), AdmissionMaxPending: maxPending, AdmissionMaxBacklogBytes: maxBytes,
	}
}

// An account's undelivered deliveries are capped by count and payload bytes;
// a refused delivery is not queued.
func TestAppWebhookOutboxRefusesAtTheAccountCap(t *testing.T) {
	m, ctx, acct, app := backlogWebhookFixture(t)
	hook := backlogWebhook(t, m, ctx, acct, app)
	for range 3 {
		if _, err := m.RecordAppWebhookDelivery(ctx, backlogDelivery(hook, `{}`, 3, 0)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := m.RecordAppWebhookDelivery(ctx, backlogDelivery(hook, `{}`, 3, 0))
	var full *state.AppWebhookOutboxFullError
	if !errors.As(err, &full) || full.Bytes || full.Limit != 3 || full.Observed != 3 {
		t.Fatalf("fourth delivery = %v, want the count cap 3/3", err)
	}
	if !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("outbox full must unwrap to ErrQuotaExceeded: %v", err)
	}
	_, err = m.RecordAppWebhookDelivery(ctx, backlogDelivery(hook, `{}`, 0, 6))
	if !errors.As(err, &full) || !full.Bytes || full.Limit != 6 || full.Observed != 6 {
		t.Fatalf("delivery past the bytes cap = %v, want 6/6 bytes", err)
	}
	// A platform event (no admission caps) still fans out.
	if _, err := m.RecordAppWebhookDelivery(ctx, backlogDelivery(hook, `{}`, 0, 0)); err != nil {
		t.Fatalf("uncapped delivery: %v", err)
	}
}

// A suspended account's deliveries wait without being claimed and resume
// when it is active again.
func TestAppWebhookClaimDefersSuspendedAccounts(t *testing.T) {
	m, ctx, acct, app := backlogWebhookFixture(t)
	hook := backlogWebhook(t, m, ctx, acct, app)
	if _, err := m.RecordAppWebhookDelivery(ctx, backlogDelivery(hook, `{}`, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if claimed, err := m.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now()); err != nil || len(claimed) != 0 {
		t.Fatalf("claim for a suspended account = %d, %v; want none", len(claimed), err)
	}
	if err := m.UpdateAccountStatus(ctx, acct.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if claimed, err := m.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now()); err != nil || len(claimed) != 1 {
		t.Fatalf("claim after reactivation = %d, %v; want 1", len(claimed), err)
	}
}

// One account holds at most api.AppWebhookMaxInFlightPerAccount live claims,
// however many subscriptions it has.
func TestAppWebhookClaimCapsInFlightPerAccount(t *testing.T) {
	m, ctx, acct, app := backlogWebhookFixture(t)
	for range 8 {
		hook := backlogWebhook(t, m, ctx, acct, app)
		for range 4 {
			if _, err := m.RecordAppWebhookDelivery(ctx, backlogDelivery(hook, `{}`, 0, 0)); err != nil {
				t.Fatal(err)
			}
		}
	}
	claimed, err := m.ClaimDueAppWebhookDeliveries(ctx, 100, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != api.AppWebhookMaxInFlightPerAccount {
		t.Fatalf("claimed %d, want the per-account cap %d", len(claimed), api.AppWebhookMaxInFlightPerAccount)
	}
	if again, err := m.ClaimDueAppWebhookDeliveries(ctx, 100, time.Now()); err != nil || len(again) != 0 {
		t.Fatalf("second claim while the account is at its cap = %d, %v; want none", len(again), err)
	}
}

// A rotation with an overlap keeps the replaced secret until the overlap
// ends; one without it, or one that keeps the same secret, keeps none.
func TestAppWebhookRotationKeepsThePreviousSecret(t *testing.T) {
	m, ctx, acct, app := backlogWebhookFixture(t)
	hook := backlogWebhook(t, m, ctx, acct, app)
	until := time.Now().Add(api.WebhookSecretRotationOverlap)
	sealed := []byte("sealed-new")
	if _, err := m.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed, PreviousSecretUntil: &until}); err != nil {
		t.Fatal(err)
	}
	previous, err := m.PreviousAppWebhookSecret(ctx, hook.ID, time.Now())
	if err != nil || string(previous) != "sealed-old" {
		t.Fatalf("previous secret = %q, %v; want the replaced secret", previous, err)
	}
	if previous, _ := m.PreviousAppWebhookSecret(ctx, hook.ID, until.Add(time.Second)); previous != nil {
		t.Fatalf("previous secret after the overlap = %q, want none", previous)
	}

	other := backlogWebhook(t, m, ctx, acct, app)
	if _, err := m.UpdateAppWebhook(ctx, other.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
		t.Fatal(err)
	}
	if previous, _ := m.PreviousAppWebhookSecret(ctx, other.ID, time.Now()); previous != nil {
		t.Fatalf("rotation without an overlap kept %q", previous)
	}
}
