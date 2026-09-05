package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// newUpgradeDashboardServer seeds one account on plan, issues a
// stepped-up dashboard session (the POST route sits behind
// requireStepUpHandler like raise-overage-cap), and installs fake as
// the billing provider. nil fake leaves the box without a provider.
func newUpgradeDashboardServer(t *testing.T, plan api.Plan, fake billing.Provider) (http.Handler, *http.Cookie, *state.MemStore, state.Account) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "upgrade@example.com", plan)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	sid := "upgrade-sid"
	if _, err := store.CreateSession(t.Context(), sid, acct.ID, "192.0.2.10", "upgrade-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stepped, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sid, acct.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue stepped-up cookie: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	if fake != nil {
		srv.WithBillingProvider(fake)
	}
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: stepped}, store, acct
}

// getUpgradePage GETs /dashboard/upgrade<query> and returns the
// recorder plus the faas_csrf cookie value (empty when the page did
// not mint one — the no-form branches).
func getUpgradePage(t *testing.T, h http.Handler, sid *http.Cookie, query string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/upgrade"+query, nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/upgrade%s: status = %d, want 200\nbody = %s", query, rec.Code, rec.Body.String())
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CookieNameAuthenticated {
			cookie = c.Value
		}
	}
	return rec, cookie
}

func TestDashboardUpgrade_GetRendersCheckoutForm(t *testing.T) {
	fake := &fakeBillingProvider{txnID: "co_1", checkoutURL: "https://polar.example/checkout/co_1"}
	h, sid, _, _ := newUpgradeDashboardServer(t, api.PlanFree, fake)
	rec, cookie := getUpgradePage(t, h, sid, "?plan=pro")
	body := rec.Body.String()
	for _, want := range []string{
		`action="/dashboard/upgrade"`,
		`name="plan" value="pro"`,
		"Continue to secure checkout",
		"Upgrade to pro",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q\nbody = %s", want, body)
		}
	}
	if cookie == "" {
		t.Fatal("page did not set the faas_csrf cookie")
	}
	if tok := extractInputValue(body, middleware.FormFieldName, "/dashboard/upgrade"); tok != cookie {
		t.Errorf("form csrf_token %q != cookie %q", tok, cookie)
	}
}

func TestDashboardUpgrade_GetWithoutPlanListsOptions(t *testing.T) {
	fake := &fakeBillingProvider{txnID: "co_1", checkoutURL: "https://polar.example/checkout/co_1"}
	h, sid, _, _ := newUpgradeDashboardServer(t, api.PlanFree, fake)
	rec, cookie := getUpgradePage(t, h, sid, "")
	body := rec.Body.String()
	for _, plan := range []string{"hobby", "pro", "scale"} {
		if !strings.Contains(body, `href="/dashboard/upgrade?plan=`+plan+`"`) {
			t.Errorf("chooser missing link for %s\nbody = %s", plan, body)
		}
	}
	if strings.Contains(body, `action="/dashboard/upgrade"`) {
		t.Errorf("chooser must not render the checkout form\nbody = %s", body)
	}
	if cookie != "" {
		t.Errorf("chooser page must not mint a csrf cookie, got %q", cookie)
	}
}

func TestDashboardUpgrade_GetExistingSubscriptionPointsAtPortal(t *testing.T) {
	fake := &fakeBillingProvider{txnID: "co_1", checkoutURL: "https://polar.example/checkout/co_1"}
	h, sid, store, acct := newUpgradeDashboardServer(t, api.PlanPro, fake)
	if err := store.UpdateAccountStripeSubscriptionItem(t.Context(), acct.ID, "sub_live"); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	rec, _ := getUpgradePage(t, h, sid, "?plan=scale")
	body := rec.Body.String()
	if strings.Contains(body, `action="/dashboard/upgrade"`) {
		t.Errorf("existing subscription must not get a hosted checkout form\nbody = %s", body)
	}
	if !strings.Contains(body, "already has a subscription") {
		t.Errorf("page missing portal explanation\nbody = %s", body)
	}
}

func TestDashboardUpgrade_PostRedirectsToProviderCheckout(t *testing.T) {
	const checkoutURL = "https://polar.example/checkout/co_1"
	fake := &fakeBillingProvider{txnID: "co_1", checkoutURL: checkoutURL, customerCreateID: "polar-cust-1"}
	h, sid, store, acct := newUpgradeDashboardServer(t, api.PlanFree, fake)
	page, cookie := getUpgradePage(t, h, sid, "?plan=pro")
	tok := extractInputValue(page.Body.String(), middleware.FormFieldName, "/dashboard/upgrade")

	rec := dashboardPOST(t, h, sid, "/dashboard/upgrade",
		map[string]string{middleware.FormFieldName: tok, "plan": "pro"},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookie})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != checkoutURL {
		t.Errorf("Location = %q, want %q", loc, checkoutURL)
	}
	if fake.customerCreateCalls != 1 || fake.calls != 1 {
		t.Errorf("provider calls: customer=%d checkout=%d, want 1/1", fake.customerCreateCalls, fake.calls)
	}
	got, err := store.AccountByID(t.Context(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.ProviderCustomerID != "polar-cust-1" {
		t.Errorf("ProviderCustomerID = %q, want polar-cust-1", got.ProviderCustomerID)
	}
	// The plan flips only on the provider webhook (spec §4.7).
	if got.Plan != api.PlanFree {
		t.Errorf("plan = %q, want free until the webhook confirms", got.Plan)
	}
	rows, err := store.ListEvents(t.Context(), acct.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found bool
	for _, e := range rows {
		if e.Kind == "billing.checkout_started" {
			found = true
		}
	}
	if !found {
		t.Errorf("billing.checkout_started audit row not found; events: %+v", rows)
	}
}

func TestDashboardUpgrade_PostRejectsBadCSRF(t *testing.T) {
	fake := &fakeBillingProvider{txnID: "co_1", checkoutURL: "https://polar.example/checkout/co_1"}
	h, sid, _, _ := newUpgradeDashboardServer(t, api.PlanFree, fake)
	_, cookie := getUpgradePage(t, h, sid, "?plan=pro")
	rec := dashboardPOST(t, h, sid, "/dashboard/upgrade",
		map[string]string{middleware.FormFieldName: "not-the-token", "plan": "pro"},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookie})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if fake.calls != 0 {
		t.Errorf("provider checkout must not run on a CSRF failure, calls = %d", fake.calls)
	}
}

func TestDashboardUpgrade_PostProviderFailureRedirectsWithNotice(t *testing.T) {
	fake := &fakeBillingProvider{err: errors.New("provider down")}
	h, sid, _, _ := newUpgradeDashboardServer(t, api.PlanFree, fake)
	page, cookie := getUpgradePage(t, h, sid, "?plan=hobby")
	tok := extractInputValue(page.Body.String(), middleware.FormFieldName, "/dashboard/upgrade")
	rec := dashboardPOST(t, h, sid, "/dashboard/upgrade",
		map[string]string{middleware.FormFieldName: tok, "plan": "hobby"},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookie})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/dashboard/billing?upgrade=error" {
		t.Errorf("Location = %q, want /dashboard/billing?upgrade=error", loc)
	}
	// The billing page turns the flag into a banner.
	bill := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/billing?upgrade=error", nil)
	req.AddCookie(sid)
	h.ServeHTTP(bill, req)
	if bill.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/billing: status = %d", bill.Code)
	}
	if !strings.Contains(bill.Body.String(), "could not start checkout") {
		t.Errorf("billing page missing the error banner\nbody = %s", bill.Body.String())
	}
}

func TestDashboardBilling_FreeRendersUpgradeLinks(t *testing.T) {
	fake := &fakeBillingProvider{txnID: "co_1", checkoutURL: "https://polar.example/checkout/co_1"}
	h, sid, _, _ := newUpgradeDashboardServer(t, api.PlanFree, fake)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/billing", nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, plan := range []string{"hobby", "pro", "scale"} {
		if !strings.Contains(body, `href="/dashboard/upgrade?plan=`+plan+`"`) {
			t.Errorf("billing page missing upgrade link for %s\nbody = %s", plan, body)
		}
	}
}

func TestDashboardBilling_NoProviderFallsBackToCLIHint(t *testing.T) {
	h, sid, _, _ := newUpgradeDashboardServer(t, api.PlanFree, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/billing", nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `href="/dashboard/upgrade?plan=`) {
		t.Errorf("no provider: billing page must not link to hosted checkout\nbody = %s", body)
	}
	if !strings.Contains(body, "faas plan") {
		t.Errorf("no provider: billing page missing the CLI hint\nbody = %s", body)
	}
}
