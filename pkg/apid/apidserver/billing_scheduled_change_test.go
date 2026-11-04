// adr: 20261103131742 — amendment 1: a subscription scheduled to end or to
// change plan can be seen and kept; Polar's refusals have their own answers.
package apidserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

const scheduledPeriodEnd = "2026-12-01T00:00:00Z"

// scheduledPolar is one Polar subscription, sub-a on the Pro product, that
// refuses what Polar refuses: a product change or a second cancellation while
// a cancellation is scheduled, and an upgrade whose charge needs the bank's
// confirmation when confirmCharge is set.
type scheduledPolar struct {
	mu             sync.Mutex
	cancelling     bool
	pendingProduct string
	confirmCharge  bool
	patches        []map[string]any
}

func (f *scheduledPolar) subscription() string {
	endsAt, pending := "null", "null"
	if f.cancelling {
		endsAt = `"` + scheduledPeriodEnd + `"`
	}
	if f.pendingProduct != "" {
		pending = `{"applies_at":"` + scheduledPeriodEnd + `","product_id":"` + f.pendingProduct + `"}`
	}
	return `{"id":"sub-a","status":"active","product_id":"pro-product","current_period_end":"` + scheduledPeriodEnd +
		`","cancel_at_period_end":` + map[bool]string{true: "true", false: "false"}[f.cancelling] +
		`,"ends_at":` + endsAt + `,"pending_update":` + pending + `}`
}

func (f *scheduledPolar) handle(t *testing.T, w http.ResponseWriter, r *http.Request) {
	const alreadyCanceled = `{"error":"AlreadyCanceledSubscription","detail":"This subscription is already canceled or will be at the end of the period."}`
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions/sub-a":
		_, _ = io.WriteString(w, f.subscription())
	case r.Method == http.MethodPatch && r.URL.Path == "/v1/subscriptions/sub-a":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.patches = append(f.patches, body)
		if cancel, ok := body["cancel_at_period_end"]; ok {
			if cancel == true && f.cancelling {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, alreadyCanceled)
				return
			}
			f.cancelling = cancel == true
		}
		if pending, ok := body["pending_update"]; ok && pending == nil {
			f.pendingProduct = ""
		}
		if product, ok := body["product_id"].(string); ok {
			switch {
			case f.cancelling:
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, alreadyCanceled)
				return
			case f.confirmCharge:
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(w, `{"error":"PaymentActionRequired","detail":"Order payment requires customer authentication (e.g. 3DS challenge); off-session charge cannot complete."}`)
				return
			case body["proration_behavior"] == "next_period":
				f.pendingProduct = product
			}
		}
		_, _ = io.WriteString(w, f.subscription())
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customer-sessions":
		_, _ = io.WriteString(w, `{"customer_portal_url":"https://polar.example/portal/session"}`)
	default:
		t.Errorf("unexpected Polar call %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

func (f *scheduledPolar) state() (cancelling bool, pending string, patches int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelling, f.pendingProduct, len(f.patches)
}

// scheduledEnv is a Pro account bound to Polar subscription sub-a.
func scheduledEnv(t *testing.T, fake *scheduledPolar) apidserver.TestEnv {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.handle(t, w, r)
	}))
	t.Cleanup(srv.Close)
	provider, err := polar.NewProvider(polar.Config{APIKey: "private-offline-scheduled-test-token", WebhookSecret: freshnessWebhookSecret,
		BaseURL: srv.URL, HobbyProductID: "hobby-product", ProProductID: "pro-product", ScaleProductID: "scale-product"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithBillingProvider(provider)
	if err := (*e.Store()).UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: e.Acct().ID, Provider: "polar", CustomerID: "cus-a", SubscriptionID: "sub-a"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func billingStatus(t *testing.T, e apidserver.TestEnv) api.BillingStatusResponse {
	t.Helper()
	rec := e.Do(t, http.MethodGet, "/v1/billing/status", nil, nil)
	var out api.BillingStatusResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("GET /v1/billing/status = %d %s", rec.Code, rec.Body)
	}
	return out
}

func scheduledProblem(t *testing.T, rec *httptest.ResponseRecorder) api.Problem {
	t.Helper()
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem from %d %s: %v", rec.Code, rec.Body, err)
	}
	return p
}

// A cancelled subscription changes no plan at Polar, and asking for the
// current plan answered 200 without undoing anything: the customer dropped to
// Free at period end. Billing status now shows the end date, another plan
// answers 409 naming how to keep the subscription, and asking for the current
// plan keeps it.
func TestKeepingTheCurrentPlanUndoesAScheduledCancellation(t *testing.T) {
	fake := &scheduledPolar{cancelling: true}
	e := scheduledEnv(t, fake)
	if got := billingStatus(t, e).SubscriptionEndsAt; got == nil || got.Format("2006-01-02") != "2026-12-01" {
		t.Fatalf("subscription_ends_at = %v, want 2026-12-01", got)
	}
	rec := e.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "scale"}, nil)
	if p := scheduledProblem(t, rec); rec.Code != http.StatusConflict || p.Code != api.CodeBillingSubscriptionEnding ||
		!strings.Contains(p.Detail, "2026-12-01") || !strings.Contains(p.Detail, `{"plan": "pro"}`) {
		t.Fatalf("upgrade while ending = %d %+v, want 409 billing_subscription_ending dated 2026-12-01 naming the keep request", rec.Code, p)
	}
	rec = e.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "pro"}, nil)
	var out api.AccountResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.PlanChangeStatus != "scheduled_change_undone" {
		t.Fatalf("keep = %d %s, want 200 scheduled_change_undone", rec.Code, rec.Body)
	}
	if cancelling, _, _ := fake.state(); cancelling {
		t.Fatal("subscription still cancelling after keep")
	}
	if billingStatus(t, e).SubscriptionEndsAt != nil {
		t.Fatal("billing status still shows an end date after keep")
	}
	_, _, before := fake.state()
	rec = e.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "pro"}, nil)
	var again api.AccountResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &again) != nil || again.PlanChangeStatus != "" {
		t.Fatalf("keep with nothing scheduled = %d %s, want 200 without a plan change status", rec.Code, rec.Body)
	}
	if _, _, after := fake.state(); after != before {
		t.Fatalf("keep with nothing scheduled sent %d subscription updates, want none", after-before)
	}
}

// A downgrade scheduled for the period end is shown, and asking for the
// current plan clears it.
func TestKeepingTheCurrentPlanClearsAScheduledDowngrade(t *testing.T) {
	fake := &scheduledPolar{}
	e := scheduledEnv(t, fake)
	if rec := e.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "hobby"}, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("downgrade = %d %s, want 202", rec.Code, rec.Body)
	}
	status := billingStatus(t, e)
	if status.ScheduledPlan != api.PlanHobby || status.ScheduledPlanAt == nil || status.SubscriptionEndsAt != nil {
		t.Fatalf("status = %+v, want hobby scheduled for the period end", status)
	}
	rec := e.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "pro"}, nil)
	var out api.AccountResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.PlanChangeStatus != "scheduled_change_undone" {
		t.Fatalf("keep = %d %s, want 200 scheduled_change_undone", rec.Code, rec.Body)
	}
	if _, pending, _ := fake.state(); pending != "" {
		t.Fatalf("pending product = %q after keep, want none", pending)
	}
	if status := billingStatus(t, e); status.ScheduledPlan != "" {
		t.Fatalf("status still shows scheduled plan %q", status.ScheduledPlan)
	}
}

// An upgrade the bank asked to confirm was reported as a declined card.
func TestUpgradeTheBankMustConfirmSaysSo(t *testing.T) {
	fake := &scheduledPolar{confirmCharge: true}
	e := scheduledEnv(t, fake)
	rec := e.Do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "scale"}, nil)
	p := scheduledProblem(t, rec)
	if rec.Code != http.StatusPaymentRequired || p.Code != api.CodePaymentAuthenticationRequired ||
		p.BillingPortalURL != "https://polar.example/portal/session" || !strings.Contains(p.Detail, "nothing was charged") {
		t.Fatalf("upgrade = %d %+v, want 402 payment_authentication_required with the portal", rec.Code, p)
	}
}

// Polar refuses a second cancellation. The cancel route answered 502 and
// deleting an account whose subscription was already cancelled failed until
// the period ended.
func TestCancellingTwiceAndDeletingAfterCancelling(t *testing.T) {
	fake := &scheduledPolar{cancelling: true}
	e := scheduledEnv(t, fake)
	rec := e.Do(t, http.MethodPost, "/v1/billing/cancel", nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second cancel = %d %s, want 409", rec.Code, rec.Body)
	}
	_, _, before := fake.state()
	if rec := e.Do(t, http.MethodDelete, "/v1/account", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("DELETE /v1/account = %d %s, want 200", rec.Code, rec.Body)
	}
	if _, _, after := fake.state(); after != before {
		t.Fatalf("deletion sent %d subscription updates to an already cancelled subscription, want none", after-before)
	}
}

// The portal link is a signed-in session that can cancel and replace the
// card; a usage:read key got it.
func TestBillingPortalNeedsAnAdminCredential(t *testing.T) {
	e := scheduledEnv(t, &scheduledPolar{})
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, hash, "usage-reader", []string{api.ScopeUsageRead}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/billing/portal", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	(*e.S()).Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("portal with a usage:read key = %d %s, want 403", w.Code, w.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/billing/portal", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("portal with the admin key = %d %s, want 200", rec.Code, rec.Body)
	}
}
