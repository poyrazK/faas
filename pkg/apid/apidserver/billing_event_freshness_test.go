package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
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
}

func newFreshnessFixture(t *testing.T) *freshnessFixture {
	t.Helper()
	f := &freshnessFixture{customer: "polar-fresh-" + uuid.NewString()}
	f.status.Store("active")
	f.product.Store("pro-product")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/subscriptions/sub-1" {
			t.Error("unexpected provider request", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		f.stateReads.Add(1)
		status := f.status.Load().(string)
		if status == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"id":"sub-1","status":"`+status+`","product_id":"`+f.product.Load().(string)+`"}`)
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
	f.h, f.store, f.account = s.Handler(), *e.Store(), e.Acct().ID
	if err := f.store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: f.account, Provider: "polar", CustomerID: f.customer, SubscriptionID: "sub-1"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *freshnessFixture) deliver(t *testing.T, eventType string, data map[string]any) {
	t.Helper()
	data["id"], data["customer_id"] = "sub-1", f.customer
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
	if w.Code != http.StatusOK {
		t.Fatalf("%s delivery = %d %s", eventType, w.Code, w.Body.String())
	}
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
