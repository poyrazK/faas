package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// newSubscriptionEndFixture seeds one account with a provider customer
// id, an optional subscription binding, and a dunning status, and
// returns a server wired with a recording mailer so the tests can call
// handleBillingEventWithOptions directly.
func newSubscriptionEndFixture(t *testing.T, plan api.Plan, status state.AccountStatus, subID string) (*server, *state.MemStore, *recordingMailer, state.Account) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "cancel@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, acct.ID, "polar-cust-1"); err != nil {
		t.Fatalf("UpdateAccountProviderCustomerID: %v", err)
	}
	if subID != "" {
		if err := store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, subID); err != nil {
			t.Fatalf("UpdateAccountStripeSubscriptionItem: %v", err)
		}
	}
	if status != state.AccountActive {
		if err := store.UpdateAccountStatus(ctx, acct.ID, status); err != nil {
			t.Fatalf("UpdateAccountStatus: %v", err)
		}
	}
	mailer := &recordingMailer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", mailer, stubGithubdClient{}, nil, nil, 0, "")
	acct, err = store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	return srv, store, mailer, acct
}

// TestSubscriptionEnded_VoluntaryDowngradesToFree pins the spec §4.7
// contract: a revoke on an active account clears the subscription
// binding, sets the plan to Free, leaves the account active, emails the
// customer once, and writes an audit row. Pre-fix the account was
// flipped to suspended with plan + subscription untouched.
func TestSubscriptionEnded_VoluntaryDowngradesToFree(t *testing.T) {
	srv, store, mailer, acct := newSubscriptionEndFixture(t, api.PlanPro, state.AccountActive, "sub_1")
	ev := billing.Event{Type: billing.EventSubscriptionCanceled, CustomerID: "polar-cust-1", SubscriptionID: "sub_1"}
	if err := srv.handleBillingEventWithOptions(context.Background(), ev, acct, false); err != nil {
		t.Fatalf("handleBillingEvent: %v", err)
	}
	got, err := store.AccountByID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Plan != api.PlanFree {
		t.Errorf("plan = %q, want free", got.Plan)
	}
	if got.StripeSubscriptionItem != "" {
		t.Errorf("subscription id = %q, want cleared", got.StripeSubscriptionItem)
	}
	if got.Status != state.AccountActive {
		t.Errorf("status = %q, want active (voluntary cancel is not a dunning event)", got.Status)
	}
	msgs := mailer.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("mails = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Subject, "Free plan") || !strings.Contains(msgs[0].TextBody, "pro") {
		t.Errorf("unexpected mail: subject=%q body=%q", msgs[0].Subject, msgs[0].TextBody)
	}
	rows, err := store.ListEvents(context.Background(), acct.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found bool
	for _, e := range rows {
		if e.Kind == "billing.subscription_ended" {
			found = true
		}
	}
	if !found {
		t.Errorf("billing.subscription_ended audit row missing; events: %+v", rows)
	}
}

// TestSubscriptionEnded_PastDueKeepsDunningStatus: a provider revoke
// after failed dunning still downgrades to Free and clears the binding,
// but the past_due stamp stays so pkg/meter.Dunning keeps its ladder.
// No extra mail — the customer already has the past_due email.
func TestSubscriptionEnded_PastDueKeepsDunningStatus(t *testing.T) {
	srv, store, mailer, acct := newSubscriptionEndFixture(t, api.PlanHobby, state.AccountPastDue, "sub_1")
	ev := billing.Event{Type: billing.EventSubscriptionCanceled, CustomerID: "polar-cust-1", SubscriptionID: "sub_1"}
	if err := srv.handleBillingEventWithOptions(context.Background(), ev, acct, false); err != nil {
		t.Fatalf("handleBillingEvent: %v", err)
	}
	got, err := store.AccountByID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Plan != api.PlanFree || got.StripeSubscriptionItem != "" {
		t.Errorf("plan/sub = %q/%q, want free/\"\"", got.Plan, got.StripeSubscriptionItem)
	}
	if got.Status != state.AccountPastDue {
		t.Errorf("status = %q, want past_due preserved for dunning", got.Status)
	}
	if n := len(mailer.snapshot()); n != 0 {
		t.Errorf("mails = %d, want 0 on the non-payment path", n)
	}
}

// TestSubscriptionEnded_IgnoresSupersededSubscription: a revoke for an
// older subscription must not touch an account that already points at
// a newer one.
func TestSubscriptionEnded_IgnoresSupersededSubscription(t *testing.T) {
	srv, store, mailer, acct := newSubscriptionEndFixture(t, api.PlanScale, state.AccountActive, "sub_new")
	ev := billing.Event{Type: billing.EventSubscriptionCanceled, CustomerID: "polar-cust-1", SubscriptionID: "sub_old"}
	if err := srv.handleBillingEventWithOptions(context.Background(), ev, acct, false); err != nil {
		t.Fatalf("handleBillingEvent: %v", err)
	}
	got, err := store.AccountByID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Plan != api.PlanScale || got.StripeSubscriptionItem != "sub_new" || got.Status != state.AccountActive {
		t.Errorf("account changed on a stale revoke: plan=%q sub=%q status=%q", got.Plan, got.StripeSubscriptionItem, got.Status)
	}
	if n := len(mailer.snapshot()); n != 0 {
		t.Errorf("mails = %d, want 0", n)
	}
}

// TestSubscriptionEnded_ReplayOnFreeIsNoop: a redelivered revoke for an
// account already on Free with no binding must be idempotent.
func TestSubscriptionEnded_ReplayOnFreeIsNoop(t *testing.T) {
	srv, store, mailer, acct := newSubscriptionEndFixture(t, api.PlanFree, state.AccountActive, "")
	ev := billing.Event{Type: billing.EventSubscriptionCanceled, CustomerID: "polar-cust-1", SubscriptionID: "sub_1"}
	if err := srv.handleBillingEventWithOptions(context.Background(), ev, acct, false); err != nil {
		t.Fatalf("handleBillingEvent: %v", err)
	}
	got, _ := store.AccountByID(context.Background(), acct.ID)
	if got.Plan != api.PlanFree || got.Status != state.AccountActive {
		t.Errorf("replay changed account: plan=%q status=%q", got.Plan, got.Status)
	}
	if n := len(mailer.snapshot()); n != 0 {
		t.Errorf("mails = %d, want 0 on replay", n)
	}
}

// TestStripeWebhook_SubscriptionDeleted_DowngradesToFree drives the same
// arm through the signed Stripe ingress so the provider → state-machine
// wiring is covered end to end (the Polar path shares
// handleBillingEventWithOptions).
func TestStripeWebhook_SubscriptionDeleted_DowngradesToFree(t *testing.T) {
	env, mailer := stripeWebhookHarness(t, api.PlanPro)
	if err := env.store.UpdateAccountStripeSubscriptionItem(context.Background(), env.acct.ID, "si_1"); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	rec := postStripeEvent(t, env.h, "customer.subscription.deleted", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	got, err := env.store.AccountByID(context.Background(), env.acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Plan != api.PlanFree || got.StripeSubscriptionItem != "" || got.Status != state.AccountActive {
		t.Errorf("after deleted: plan=%q sub=%q status=%q, want free/\"\"/active", got.Plan, got.StripeSubscriptionItem, got.Status)
	}
	if n := len(mailer.snapshot()); n != 1 {
		t.Errorf("mails = %d, want 1", n)
	}
}
