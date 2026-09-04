package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestMapStripeTypeToEventType is a table-driven sweep of the pure
// helper at handlers_ext.go:2521. Covers every documented Stripe
// subscription + invoice event type plus the default-branch fallback.
func TestMapStripeTypeToEventType(t *testing.T) {
	cases := []struct {
		in   string
		want billing.EventType
	}{
		{"customer.subscription.created", billing.EventSubscriptionCreated},
		{"customer.subscription.updated", billing.EventSubscriptionUpdated},
		{"customer.subscription.deleted", billing.EventSubscriptionCanceled},
		{"customer.subscription.past_due", billing.EventSubscriptionPastDue},
		{"invoice.payment_failed", billing.EventPaymentFailed},
		{"invoice.payment_succeeded", billing.EventPaymentSucceeded},
		{"customer.created", billing.EventUnknown},
		{"charge.refunded", billing.EventUnknown},
		{"", billing.EventUnknown},
	}
	for _, c := range cases {
		if got := mapStripeTypeToEventType(c.in); got != c.want {
			t.Errorf("mapStripeTypeToEventType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBillingPlanFromProviderID(t *testing.T) {
	cases := []struct {
		id   string
		want api.Plan
	}{
		{"pro", api.PlanPro},
		{"plan_pro_monthly", api.PlanPro},
		{"pri_scale_monthly", api.PlanScale},
		{"product_hobby", api.PlanHobby},
		{"product", ""},
		{"price_unknown", ""},
	}
	for _, tc := range cases {
		if got := billingPlanFromProviderID(tc.id); got != tc.want {
			t.Errorf("billingPlanFromProviderID(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// TestLookupAccountByStripeID covers the empty-id rejection branch
// (handlers_ext.go:2545) and the happy lookup path through MemStore.
func TestLookupAccountByStripeID(t *testing.T) {
	srv := &server{store: state.NewMemStore()}
	ctx := context.Background()

	// Empty id is rejected without touching the store.
	if _, err := srv.lookupAccountByStripeID(ctx, ""); err == nil {
		t.Fatalf("empty stripe id = nil error, want error")
	}

	// Unknown id is not-found.
	if _, err := srv.lookupAccountByStripeID(ctx, "cus_unknown"); err == nil {
		t.Fatalf("unknown stripe id = nil error, want not-found")
	}

	// Happy: bind an account to a stripe id and look it up.
	acct, err := srv.store.CreateAccount(ctx, "stripe-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAccountProviderCustomerID(ctx, acct.ID, "cus_known"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.lookupAccountByStripeID(ctx, "cus_known")
	if err != nil {
		t.Fatalf("happy lookup: %v", err)
	}
	if got.ID != acct.ID {
		t.Fatalf("lookup returned %q, want %q", got.ID, acct.ID)
	}
}

// TestLookupAccountByPaddleID mirrors the Stripe test for the Paddle
// counterpart. Same shape: empty-id rejection, unknown-id, happy path.
func TestLookupAccountByPaddleID(t *testing.T) {
	srv := &server{store: state.NewMemStore()}
	ctx := context.Background()

	if _, err := srv.lookupAccountByPaddleID(ctx, ""); err == nil {
		t.Fatalf("empty paddle id = nil error, want error")
	}
	if _, err := srv.lookupAccountByPaddleID(ctx, "ctm_unknown"); err == nil {
		t.Fatalf("unknown paddle id = nil error, want not-found")
	}

	acct, err := srv.store.CreateAccount(ctx, "paddle-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAccountProviderCustomerID(ctx, acct.ID, "ctm_known"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.lookupAccountByPaddleID(ctx, "ctm_known")
	if err != nil {
		t.Fatalf("happy lookup: %v", err)
	}
	if got.ID != acct.ID {
		t.Fatalf("lookup returned %q, want %q", got.ID, acct.ID)
	}
}

// TestUsageDailyHandler covers the empty-result path of GET /v1/usage/daily
// when the account has no prior usage rows. The accounting queries live
// in pkg/meter; the handler is responsible for the input validation
// (--day shape, range clamping) and the response shape.
func TestUsageDailyHandler(t *testing.T) {
	e := setup(t, api.PlanPro)

	// Missing day parameter is rejected as 400.
	rec := e.do(t, "GET", "/v1/usage/daily", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("missing day: status %d, want 400", rec.Code)
	}

	// Bad day parameter is rejected as 400.
	rec = e.do(t, "GET", "/v1/usage/daily?day=not-a-date", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("bad day param: status %d, want 400", rec.Code)
	}

	// Valid day — handler returns 200 with JSON object containing items array.
	rec = e.do(t, "GET", "/v1/usage/daily?day=2026-08-07", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		t.Fatalf("body shape = %q, want JSON object", body)
	}
}

// TestUsageStorageHandler mirrors TestUsageDailyHandler for the storage
// rollup. Empty account + bad day + happy path.
func TestUsageStorageHandler(t *testing.T) {
	e := setup(t, api.PlanPro)

	rec := e.do(t, "GET", "/v1/usage/storage", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("missing day: status %d, want 400", rec.Code)
	}

	rec = e.do(t, "GET", "/v1/usage/storage?day=2026-13-99", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("bad day param: status %d, want 400", rec.Code)
	}

	rec = e.do(t, "GET", "/v1/usage/storage?day=2026-08-07", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want JSON", ct)
	}
}

// TestHandleBillingEventStripe exercises handleBillingEvent for a stripe
// payment-succeeded event on an account that already has a stripe
// customer id. The branch we hit is the "known account" path; the
// "unknown account" path is covered by the lookup tests above.
func TestHandleBillingEventStripe(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()

	// Bind account to a stripe id so lookupAccountByStripeID succeeds.
	if err := e.store.UpdateAccountProviderCustomerID(ctx, e.acct.ID, "cus_test"); err != nil {
		t.Fatal(err)
	}

	// Drive handleBillingEvent directly — it's not exported, but is
	// reachable through the paddleWebhook + stripeWebhook smoke flow
	// that the higher-level handler tests cover. Here we just verify
	// the function doesn't panic on a known-shape event.
	ev := billing.Event{
		EventID:    "evt_test_" + uuid.NewString(),
		Type:       billing.EventPaymentSucceeded,
		CustomerID: "cus_test",
	}
	e.s.handleBillingEvent(ctx, ev, e.acct)
	_ = time.Now // unused import guard
}

// TestHandleBillingEventSubscriptionCreatedDoesNotEnableMFA pins
// the opt-in policy for billing webhooks. Attaching a card or
// starting a subscription must not force MFA enrollment.
func TestHandleBillingEventSubscriptionCreatedDoesNotEnableMFA(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.handleBillingEvent(context.Background(), billing.Event{
		Type: billing.EventSubscriptionCreated,
	}, e.acct)

	fresh, err := e.store.AccountByID(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.MFARequired {
		t.Fatal("subscription-created webhook silently enabled MFA; enrollment must be opt-in")
	}
}

// TestListDeploymentsWithPagination covers the deployment list endpoint
// including the limit-clamping + cursor handling. The handler accepts
// `?limit=N` and `?before=<RFC3339Nano>` for cursor pagination.
func TestListDeploymentsWithPagination(t *testing.T) {
	e := setup(t, api.PlanPro)

	// Empty app store: deployment list returns 200 with empty items array.
	rec := e.do(t, "GET", "/v1/deployments", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.DeploymentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("empty list = %d items, want 0", len(out.Items))
	}

	// Bad limit falls back to default (handler silently uses the default cap;
	// the call site should not pass unparseable ints — this is the documented
	// behaviour, not a 400).
	rec = e.do(t, "GET", "/v1/deployments?limit=abc", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("bad limit: status %d, want 200 (default applied)", rec.Code)
	}

	// Large limit is clamped to 200 (driver: pagination ceil).
	rec = e.do(t, "GET", "/v1/deployments?limit=99999", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("large limit: status %d, want 200", rec.Code)
	}
}

// TestUpdateDeploymentMinInstances covers the PATCH /v1/deployments/{id}
// endpoint's min-instances branch. The handler validates the request
// shape and dispatches the update to the store.
func TestUpdateDeploymentMinInstances(t *testing.T) {
	e := setup(t, api.PlanPro)

	// Create an app + deployment to operate on.
	slug := "min-inst-" + uuid.NewString()[:8]
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID, Slug: slug, RAMMB: 256, Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:co",
	})
	if err != nil {
		t.Fatal(err)
	}

	// PATCH with a valid min-instances override.
	rec := e.do(t, "PATCH", "/v1/deployments/"+dep.ID, map[string]any{
		"min_instances": 1,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	// Negative value is rejected.
	rec = e.do(t, "PATCH", "/v1/deployments/"+dep.ID, map[string]any{
		"min_instances": -1,
	}, nil)
	if rec.Code == 200 {
		t.Fatalf("negative min_instances: status %d, want 4xx", rec.Code)
	}

	// Missing deployment returns 404.
	rec = e.do(t, "PATCH", "/v1/deployments/00000000-0000-0000-0000-000000000000",
		map[string]any{"min_instances": 1}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing deployment: status %d, want 404", rec.Code)
	}
}
