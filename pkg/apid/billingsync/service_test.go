package billingsync_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/billingsync"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeHost reconciles MemStore accounts against scripted subscriptions and
// records the corrections instead of applying them.
type fakeHost struct {
	store    *state.MemStore
	subs     map[string][]billing.Subscription // by customer
	applied  []billing.Event
	applyErr error
	audits   int
}

func (h *fakeHost) Store() state.Store { return h.store }

func (h *fakeHost) Provider() (string, billing.CustomerSubscriptionLister, bool) {
	return "polar", h, true
}

func (h *fakeHost) CustomerSubscriptions(_ context.Context, customerID string) ([]billing.Subscription, error) {
	return h.subs[customerID], nil
}

func (h *fakeHost) Account(ctx context.Context, provider, customerID string) (state.Account, error) {
	acct, err := h.store.AccountByBillingCustomerID(ctx, provider, customerID)
	if err != nil {
		return acct, err
	}
	identity, err := h.store.BillingIdentity(ctx, acct.ID, provider)
	acct.ProviderCustomerID, acct.StripeSubscriptionItem = identity.CustomerID, identity.SubscriptionID
	return acct, err
}

func (h *fakeHost) PlanFromProviderID(id string) api.Plan { return api.Plan(id) }

func (h *fakeHost) Apply(_ context.Context, ev billing.Event, _ state.Account) error {
	if h.applyErr != nil {
		return h.applyErr
	}
	h.applied = append(h.applied, ev)
	return nil
}

func (h *fakeHost) Audit(context.Context, string, string, map[string]any) { h.audits++ }

// newHost binds n Pro accounts to Polar subscriptions the provider reports
// as ended, so every account differs.
func newHost(t *testing.T, n int) *fakeHost {
	t.Helper()
	h := &fakeHost{store: state.NewMemStore(), subs: map[string][]billing.Subscription{}}
	for i := range n {
		acct, err := h.store.CreateAccount(t.Context(), "billing-sync-"+strconv.Itoa(i)+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		customer, subID := "cus-"+acct.ID, "sub-"+acct.ID
		if err := h.store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: customer, SubscriptionID: subID}); err != nil {
			t.Fatal(err)
		}
		h.subs[customer] = []billing.Subscription{sub(subID, "canceled", "pro")}
	}
	return h
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// A difference is corrected only when the next pass still sees it: a webhook
// delivery in flight settles it first, and a settled one is never applied.
func TestReconcileCorrectsOnlyPersistentDifferences(t *testing.T) {
	h := newHost(t, 2)
	svc := &billingsync.Service{Host: h}
	res, err := svc.RunOnce(t.Context(), discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 2 || res.Deferred != 2 || res.Corrected != 0 || len(h.applied) != 0 {
		t.Fatalf("first pass = %+v applied %d, want both deferred", res, len(h.applied))
	}
	// One account's delivery lands between the passes.
	for customer := range h.subs {
		h.subs[customer][0].Status = billing.SubscriptionStatusActive
		break
	}
	res, err = svc.RunOnce(t.Context(), discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Corrected != 1 || len(h.applied) != 1 || h.audits != 1 {
		t.Fatalf("second pass = %+v applied %d audits %d, want one correction", res, len(h.applied), h.audits)
	}
	ev := h.applied[0]
	if ev.Type != billing.EventSubscriptionCanceled || ev.EventID == "" || ev.CustomerID == "" {
		t.Fatalf("correction = %+v, want a subscription_canceled delivery with an event and customer id", ev)
	}
}

func TestReconcileCapsCorrectionsPerPass(t *testing.T) {
	h := newHost(t, api.BillingReconcileMaxCorrectionsPerPass+2)
	svc := &billingsync.Service{Host: h}
	for range 2 {
		if _, err := svc.RunOnce(t.Context(), discard); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.applied) != api.BillingReconcileMaxCorrectionsPerPass {
		t.Fatalf("second pass applied %d, want the cap %d", len(h.applied), api.BillingReconcileMaxCorrectionsPerPass)
	}
	res, err := svc.RunOnce(t.Context(), discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Corrected != 2 {
		t.Fatalf("third pass corrected %d, want the 2 held back by the cap", res.Corrected)
	}
}

// A failed correction is retried by the next pass without a fresh
// confirmation, and does not stop the pass.
func TestReconcileRetriesFailedCorrections(t *testing.T) {
	h := newHost(t, 1)
	svc := &billingsync.Service{Host: h}
	if _, err := svc.RunOnce(t.Context(), discard); err != nil {
		t.Fatal(err)
	}
	h.applyErr = errors.New("database unavailable")
	res, err := svc.RunOnce(t.Context(), discard)
	if err != nil || res.Failed != 1 {
		t.Fatalf("pass = %+v, %v; want one failed account and no pass error", res, err)
	}
	h.applyErr = nil
	if res, err = svc.RunOnce(t.Context(), discard); err != nil || res.Corrected != 1 {
		t.Fatalf("retry pass = %+v, %v; want the correction applied", res, err)
	}
}

type noListerHost struct{ *fakeHost }

func (noListerHost) Provider() (string, billing.CustomerSubscriptionLister, bool) {
	return "", nil, false
}

func TestReconcileIdleWithoutAListingProvider(t *testing.T) {
	h := newHost(t, 1)
	res, err := (&billingsync.Service{Host: noListerHost{h}}).RunOnce(t.Context(), discard)
	if err != nil || res.Checked != 0 {
		t.Fatalf("pass = %+v, %v; want nothing checked", res, err)
	}
}
