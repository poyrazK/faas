package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/billingsync"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

const freshnessWebhookSecret = "polar_whs_private-freshness-test-secret"

// freshnessFixture is a Polar-billed Pro account whose provider API reports
// the subscription state the test sets.
type freshnessFixture struct {
	h          http.Handler
	store      *state.MemStore
	account    string
	customer   string
	status     atomic.Value // string; empty means the provider read fails
	product    atomic.Value // string
	stateReads atomic.Int32
	sync       *billingsync.Service
}

func newFreshnessFixture(t *testing.T) *freshnessFixture {
	t.Helper()
	f := &freshnessFixture{customer: "polar-fresh-" + uuid.NewString()}
	f.status.Store("active")
	f.product.Store("pro-product")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := f.status.Load().(string)
		subscription := `{"id":"sub-1","status":"` + status + `","product_id":"` + f.product.Load().(string) + `"}`
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions/" && r.URL.Query().Get("customer_id") == f.customer:
			// Reconciliation's listing; an empty status fails the read
			// (without the transport's retries).
			if status == "" {
				http.Error(w, "rejected", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"items":[`+subscription+`],"pagination":{"total_count":1,"max_page":1}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions/sub-1":
			f.stateReads.Add(1)
			if status == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, subscription)
		default:
			t.Error("unexpected provider request", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	provider, err := polar.NewProvider(polar.Config{APIKey: "private-offline-freshness-test-token", WebhookSecret: freshnessWebhookSecret,
		BaseURL: upstream.URL, HobbyProductID: "hobby-product", ProProductID: "pro-product", ScaleProductID: "scale-product"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := apidserver.Setup(t, api.PlanPro)
	s := *e.S()
	s.WithBillingProvider(provider)
	f.h, f.store, f.account, f.sync = s.Handler(), *e.Store(), e.Acct().ID, s.BillingSync()
	if err := f.store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: f.account, Provider: "polar", CustomerID: f.customer, SubscriptionID: "sub-1"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *freshnessFixture) deliver(t *testing.T, eventType string, data map[string]any) {
	t.Helper()
	if code := f.deliverAs(t, f.customer, eventType, data); code != http.StatusOK {
		t.Fatalf("%s delivery = %d", eventType, code)
	}
}

func (f *freshnessFixture) deliverAs(t *testing.T, customer, eventType string, data map[string]any) int {
	t.Helper()
	data["id"], data["customer_id"] = "sub-1", customer
	body, err := json.Marshal(map[string]any{"type": eventType, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	when, id := time.Now(), uuid.NewString()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/polar", bytes.NewReader(body))
	r.Header.Set("webhook-id", id)
	r.Header.Set("webhook-timestamp", strconv.FormatInt(when.Unix(), 10))
	r.Header.Set("webhook-signature", polar.SignForTest(body, freshnessWebhookSecret, id, when))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w.Code
}

func (f *freshnessFixture) loadAccount(t *testing.T) state.Account {
	t.Helper()
	acct, err := f.store.AccountByID(t.Context(), f.account)
	if err != nil {
		t.Fatal(err)
	}
	return acct
}

// Hunt 17 H17-1: a payment failure retried after the customer paid must not
// restart dunning. Polar does not order deliveries, and any 503 on the first
// attempt (a database blip, a deploy, a held mail recovery) delays it past the
// payment. The subscription's current state decides.
func TestPolarPaymentFailureFollowsCurrentSubscriptionState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		wantStatus state.AccountStatus
	}{
		{name: "retried after payment", status: "active", wantStatus: state.AccountActive},
		{name: "trialing", status: "trialing", wantStatus: state.AccountActive},
		{name: "still overdue", status: "past_due", wantStatus: state.AccountPastDue},
		// A failed provider read keeps today's behaviour: the delivery applies.
		{name: "state unavailable", status: "", wantStatus: state.AccountPastDue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFreshnessFixture(t)
			f.status.Store(tc.status)
			f.deliver(t, "subscription.past_due", map[string]any{"status": "past_due"})
			if got := f.loadAccount(t); got.Status != tc.wantStatus {
				t.Fatalf("account status = %s, want %s", got.Status, tc.wantStatus)
			}
			if f.stateReads.Load() == 0 {
				t.Fatal("the delivery was applied without reading the subscription's current state")
			}
		})
	}
}

// A subscription update grants the subscription's current product, so a late
// update carrying an earlier product cannot revert a plan change, and an
// update for a subscription that has since ended changes nothing.
func TestPolarSubscriptionUpdateGrantsCurrentPlan(t *testing.T) {
	f := newFreshnessFixture(t)
	f.deliver(t, "subscription.updated", map[string]any{"status": "active", "product_id": "hobby-product"})
	if got := f.loadAccount(t); got.Plan != api.PlanPro {
		t.Fatalf("plan = %s after a stale update carrying hobby, want the current pro", got.Plan)
	}
	f.product.Store("scale-product")
	f.deliver(t, "subscription.updated", map[string]any{"status": "active", "product_id": "scale-product"})
	if got := f.loadAccount(t); got.Plan != api.PlanScale {
		t.Fatalf("plan = %s, want scale", got.Plan)
	}
	f.status.Store("canceled")
	f.deliver(t, "subscription.updated", map[string]any{"status": "active", "product_id": "hobby-product"})
	if got := f.loadAccount(t); got.Plan != api.PlanScale {
		t.Fatalf("plan = %s after an update for an ended subscription, want scale unchanged", got.Plan)
	}
}

// A payment success delivered while the provider still reports the
// subscription past due is a late delivery for an earlier payment: it must not
// end dunning on an account that still owes money.
func TestPolarPaymentSuccessDoesNotEndDunningWhileOverdue(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		wantStatus state.AccountStatus
	}{
		{name: "still overdue", status: "past_due", wantStatus: state.AccountPastDue},
		{name: "paid up", status: "active", wantStatus: state.AccountActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFreshnessFixture(t)
			f.status.Store("past_due")
			f.deliver(t, "subscription.past_due", map[string]any{"status": "past_due"})
			if got := f.loadAccount(t); got.Status != state.AccountPastDue {
				t.Fatalf("account status = %s, want past_due", got.Status)
			}
			f.status.Store(tc.status)
			f.deliver(t, "subscription.active", map[string]any{"status": "active", "product_id": "pro-product"})
			got := f.loadAccount(t)
			if got.Status != tc.wantStatus {
				t.Fatalf("account status = %s, want %s", got.Status, tc.wantStatus)
			}
			if got.Plan != api.PlanPro {
				t.Fatalf("plan = %s, want pro kept", got.Plan)
			}
		})
	}
}

// A delivery for a customer no account is bound to is acknowledged: Polar
// would redeliver it in vain and disable the endpoint after 10 consecutive
// failures, stopping every account's webhooks.
func TestPolarDeliveryForAnUnknownCustomerIsAcknowledged(t *testing.T) {
	f := newFreshnessFixture(t)
	for _, eventType := range []string{"subscription.revoked", "subscription.active", "order.paid"} {
		if code := f.deliverAs(t, "polar-unknown-"+uuid.NewString(), eventType, map[string]any{"status": "active"}); code != http.StatusOK {
			t.Fatalf("%s for an unknown customer = %d, want 200", eventType, code)
		}
	}
	if got := f.loadAccount(t); got.Plan != api.PlanPro || got.Status != state.AccountActive {
		t.Fatalf("bound account changed to %s/%s", got.Plan, got.Status)
	}
}

// Reconciliation repairs an account whose webhook deliveries never arrived:
// two passes that agree apply the transition the delivery would have.
func TestReconcileAppliesMissedPolarTransitions(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pass := func(t *testing.T, f *freshnessFixture) billingsync.Result {
		t.Helper()
		res, err := f.sync.RunOnce(t.Context(), log)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	t.Run("missed revoke", func(t *testing.T) {
		f := newFreshnessFixture(t)
		f.status.Store("canceled")
		if res := pass(t, f); res.Deferred != 1 || f.loadAccount(t).Plan != api.PlanPro {
			t.Fatalf("first pass = %+v, want the downgrade deferred", res)
		}
		if res := pass(t, f); res.Corrected != 1 {
			t.Fatalf("second pass = %+v, want one correction", res)
		}
		identity, err := f.store.BillingIdentity(t.Context(), f.account, "polar")
		if err != nil {
			t.Fatal(err)
		}
		if got := f.loadAccount(t); got.Plan != api.PlanFree || identity.SubscriptionID != "" {
			t.Fatalf("after reconciliation: plan %s, subscription %q; want free and unbound", got.Plan, identity.SubscriptionID)
		}
	})
	t.Run("missed payment failure then recovery", func(t *testing.T) {
		f := newFreshnessFixture(t)
		f.status.Store("past_due")
		pass(t, f)
		pass(t, f)
		if got := f.loadAccount(t); got.Status != state.AccountPastDue {
			t.Fatalf("status = %s, want past_due", got.Status)
		}
		f.status.Store("active")
		pass(t, f)
		pass(t, f)
		if got := f.loadAccount(t); got.Status != state.AccountActive || got.Plan != api.PlanPro {
			t.Fatalf("after recovery: %s/%s, want active pro", got.Status, got.Plan)
		}
	})
	t.Run("missed plan change", func(t *testing.T) {
		f := newFreshnessFixture(t)
		f.product.Store("scale-product")
		pass(t, f)
		pass(t, f)
		if got := f.loadAccount(t); got.Plan != api.PlanScale {
			t.Fatalf("plan = %s, want scale", got.Plan)
		}
	})
	t.Run("provider unavailable", func(t *testing.T) {
		f := newFreshnessFixture(t)
		f.status.Store("")
		if res := pass(t, f); res.Failed != 1 {
			t.Fatalf("pass = %+v, want the account failed and retried later", res)
		}
		if got := f.loadAccount(t); got.Plan != api.PlanPro || got.Status != state.AccountActive {
			t.Fatalf("account changed to %s/%s on a failed read", got.Plan, got.Status)
		}
	})
}
