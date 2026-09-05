package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func newAccountPlanDashboardServer(t *testing.T, plan api.Plan, portalURL string, provider *fakeBillingProvider) (http.Handler, *http.Cookie, *state.MemStore, state.Account) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "plan@example.com", plan)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	sid := "plan-sid"
	if _, err := store.CreateSession(t.Context(), sid, acct.ID, "192.0.2.10", "plan-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stepped, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sid, acct.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue stepped-up cookie: %v", err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	if provider != nil {
		srv.WithBillingProvider(provider)
	}
	if portalURL != "" {
		srv.WithBillingPortalURL(portalURL)
	}
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: stepped}, store, acct
}

func renderAccountPlan(t *testing.T, h http.Handler, sid *http.Cookie) (*http.Cookie, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account", nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/account: status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	var csrfCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == dashboardAccountPlanCSRFCookie {
			csrfCookie = cookie
			break
		}
	}
	if csrfCookie == nil {
		t.Fatalf("GET /dashboard/account: missing %s cookie", dashboardAccountPlanCSRFCookie)
	}
	token := extractInputValue(rec.Body.String(), middleware.FormFieldName, "/dashboard/account/plan")
	if token == "" {
		t.Fatal("account plan form is missing csrf_token")
	}
	return csrfCookie, token, rec.Body.String()
}

func TestDashboardAccountPlan_RendersNamedCSRF(t *testing.T) {
	h, sid, _, _ := newAccountPlanDashboardServer(t, api.PlanFree, "", nil)
	csrfCookie, token, body := renderAccountPlan(t, h, sid)
	if csrfCookie.Value != token {
		t.Fatalf("plan csrf cookie %q != form token %q", csrfCookie.Value, token)
	}
	if !strings.Contains(body, `<select name="plan" required>`) {
		t.Fatalf("account plan selector is not required\nbody = %s", body)
	}
}

func TestDashboardAccountPlan_FreePaidRedirectsToCheckoutPreview(t *testing.T) {
	provider := &fakeBillingProvider{txnID: "checkout-1", checkoutURL: "https://billing.example/checkout/1"}
	h, sid, store, acct := newAccountPlanDashboardServer(t, api.PlanFree, "", provider)
	csrfCookie, token, _ := renderAccountPlan(t, h, sid)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/plan", map[string]string{
		middleware.FormFieldName: token,
		"plan":                   "pro",
	}, csrfCookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/upgrade?plan=pro" {
		t.Fatalf("Location = %q, want checkout preview", got)
	}
	if provider.calls != 0 {
		t.Fatalf("account form must not start checkout before preview, calls = %d", provider.calls)
	}
	got, err := store.AccountByID(t.Context(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Plan != api.PlanFree {
		t.Fatalf("plan = %q, want free until provider confirmation", got.Plan)
	}
}

func TestDashboardAccountPlan_PaidDowngradeRedirectsToPortal(t *testing.T) {
	const portalTemplate = "https://billing.example/portal?account={account_id}"
	h, sid, store, acct := newAccountPlanDashboardServer(t, api.PlanPro, portalTemplate, nil)
	if err := store.UpdateAccountStripeSubscriptionItem(t.Context(), acct.ID, "sub_live"); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	csrfCookie, token, _ := renderAccountPlan(t, h, sid)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/plan", map[string]string{
		middleware.FormFieldName: token,
		"plan":                   "free",
	}, csrfCookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	want := "https://billing.example/portal?account=" + acct.ID
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	got, err := store.AccountByID(t.Context(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Plan != api.PlanPro {
		t.Fatalf("plan = %q, want pro until provider confirmation", got.Plan)
	}
}

func TestDashboardAccountPlan_RejectsInvalidCSRF(t *testing.T) {
	h, sid, _, _ := newAccountPlanDashboardServer(t, api.PlanFree, "", nil)
	csrfCookie, _, _ := renderAccountPlan(t, h, sid)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/plan", map[string]string{
		middleware.FormFieldName: "wrong-token",
		"plan":                   "pro",
	}, csrfCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}
