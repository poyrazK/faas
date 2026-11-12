package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the billing operations exercised here are implemented. An unexpected
// external operation fails the test instead of contacting a real provider.
type customerMFABilling struct {
	billing.Provider
	checkouts, cancellations int
}

func (*customerMFABilling) Capabilities() billing.CapabilitySet {
	return billing.CapabilitySet(billing.CapHostedCheckout)
}
func (*customerMFABilling) CreateCustomer(context.Context, state.Account) (string, error) {
	return "customer-mfa", nil
}
func (p *customerMFABilling) CreateUpgradeTransaction(context.Context, state.Account, api.Plan) (string, string, error) {
	p.checkouts++
	return "checkout-mfa", "https://billing.example/checkout", nil
}
func (p *customerMFABilling) CancelAtPeriodEnd(context.Context, state.Account) (time.Time, error) {
	p.cancellations++
	return time.Now().Add(24 * time.Hour), nil
}
func (*customerMFABilling) CreateCustomerPortalSession(context.Context, state.Account, string) (string, error) {
	return "https://billing.example/portal", nil
}
func (*customerMFABilling) PaymentMethodSummary(context.Context, state.Account) (billing.PaymentMethod, error) {
	return billing.PaymentMethod{}, nil
}

func customerMFARequest(t *testing.T, h http.Handler, cookie *http.Cookie, method, path string, body any, extra ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	req.AddCookie(cookie)
	for _, c := range extra {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCustomerMFAOptionalBillingAndCredentials(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanFree, false, false)
	st, acct := *e.Store(), *e.Acct()
	if err := st.MarkAccountEmailVerified(t.Context(), acct.ID); err != nil {
		t.Fatal(err)
	}
	provider := &customerMFABilling{}
	(*e.Srv()).WithBillingProvider(provider)
	(*e.Srv()).WithBillingProviderName("polar")
	if err := st.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "customer-mfa"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"PATCH", "/v1/account/plan", map[string]string{"plan": "hobby"}, 402},
		{"PATCH", "/v1/account/billing", map[string]string{"billing_address": "Corrected address"}, 200},
		{"GET", "/v1/billing/portal", nil, 200},
		{"POST", "/v1/billing/cancel", nil, 200},
		{"POST", "/v1/keys", map[string]any{"name": "customer-key", "scopes": []string{"admin"}}, 201},
		{"POST", "/v1/account/overage-cap", map[string]any{"overage_cap_cents": 100}, 200},
	} {
		rec := customerMFARequest(t, *e.H(), *e.Cookie(), tc.method, tc.path, tc.body)
		if rec.Code != tc.want || strings.Contains(rec.Body.String(), "step_up_required") {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	if provider.checkouts != 1 || provider.cancellations != 1 {
		t.Fatalf("billing calls: checkout=%d cancel=%d", provider.checkouts, provider.cancellations)
	}
}

func TestCustomerMFAOnlyActualPlanChangesNeedFreshProof(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanFree, false, false)
	if err := (*e.Store()).MarkAccountEmailVerified(t.Context(), e.Acct().ID); err != nil {
		t.Fatal(err)
	}
	_, _, fresh := e.GenerateEnrolledAccount(t)
	mgr := *(*e.Srv()).Sessions()
	env, err := mgr.Verify(fresh.Value)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := mgr.IssueWithSessionAndBindingHashAndStepUp(env.Sid, env.AccountID, env.BindingHash, time.Now().Add(-time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: apidserver.SessionCookie, Value: stale}
	csrf, err := middleware.IssueForAuthenticatedNamed(mgr, "account_plan", e.Acct().ID, "faas_csrf_account_plan")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"free", "hobby"} {
		form := url.Values{"plan": {target}, "csrf_token": {csrf}}
		req := httptest.NewRequest("POST", "/dashboard/account/plan", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: "faas_csrf_account_plan", Value: csrf})
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		if rec.Code != 303 {
			t.Fatalf("unchanged/preview %s = %d %s", target, rec.Code, rec.Body)
		}
	}
	refused := customerMFARequest(t, *e.H(), cookie, "POST", "/v1/keys", map[string]any{"name": "stale", "scopes": []string{"admin"}})
	apidserver.AssertProblem(t, refused, 403, api.CodeStepUpRequired)
}

func TestCustomerMFABillingSuspensionAllowsSetupAndRecovery(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanFree, false, false)
	st, acct := *e.Store(), *e.Acct()
	if err := st.MarkAccountEmailVerified(t.Context(), acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateAccountStatus(t.Context(), acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	provider := &customerMFABilling{}
	(*e.Srv()).WithBillingProvider(provider)
	(*e.Srv()).WithBillingProviderName("polar")
	if err := st.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "customer-mfa"}); err != nil {
		t.Fatal(err)
	}
	paid := customerMFARequest(t, *e.H(), *e.Cookie(), "PATCH", "/v1/account/plan", map[string]string{"plan": "hobby"})
	apidserver.AssertProblem(t, paid, 402, api.CodePayment)
	codes, _, confirmed := e.GenerateEnrolledAccount(t)
	*e.Cookie() = confirmed
	csrf, cookies := accountSecurityCSRF(t, *e.H(), confirmed, "mfa_recover")
	recovered := customerMFARequest(t, *e.H(), confirmed, "POST", "/v1/account/mfa/recover", api.MFARecoverRequest{Code: codes[0], CSRFToken: csrf}, cookies...)
	if recovered.Code != 200 {
		t.Fatalf("suspended recovery = %d %s", recovered.Code, recovered.Body)
	}
	cookie := accountSecurityCookie(recovered)
	mgr := *(*e.Srv()).Sessions()
	env, err := mgr.Verify(cookie.Value)
	if err != nil || env.MfaPending || env.StepUpAt.IsZero() {
		t.Fatalf("recovery did not establish recent verification: %v", err)
	}
	portal := customerMFARequest(t, *e.H(), cookie, "GET", "/v1/billing/portal", nil)
	if portal.Code != 200 {
		t.Fatalf("portal after recovery = %d %s", portal.Code, portal.Body)
	}
	csrf, cookies = accountSecurityCSRF(t, *e.H(), cookie, "mfa_disable")
	disabled := customerMFARequest(t, *e.H(), cookie, "POST", "/v1/account/mfa/disable", api.MFADisableRequest{RecoveryCode: codes[1], CSRFToken: csrf}, cookies...)
	if disabled.Code != 200 {
		t.Fatalf("suspended factor reset = %d %s", disabled.Code, disabled.Body)
	}
}

func TestCustomerMFADisableUpdatesPendingSession(t *testing.T) {
	for _, policy := range []bool{false, true} {
		e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
		codes, _, _ := e.GenerateEnrolledAccount(t)
		if policy {
			if _, err := (*e.Store()).SetMFARequired(t.Context(), e.Acct().ID, true); err != nil {
				t.Fatal(err)
			}
		}
		cookie := e.MfaIssueWithPending(t, true)
		csrf, cookies := accountSecurityCSRF(t, *e.H(), cookie, "mfa_disable")
		rec := customerMFARequest(t, *e.H(), cookie, "POST", "/v1/account/mfa/disable", api.MFADisableRequest{RecoveryCode: codes[0], CSRFToken: csrf}, cookies...)
		if rec.Code != 200 {
			t.Fatalf("reset = %d %s", rec.Code, rec.Body)
		}
		cleared := accountSecurityCookie(rec)
		if cleared == nil {
			t.Fatal("factor reset did not update the browser session")
		}
		env, err := (*(*e.Srv()).Sessions()).Verify(cleared.Value)
		if err != nil || env.MfaPending != policy {
			t.Fatalf("policy=%v pending=%v err=%v", policy, env.MfaPending, err)
		}
		// Other tabs may still send the old pending envelope after a reset.
		view := customerMFARequest(t, *e.H(), cookie, "GET", "/dashboard/account", nil)
		if policy {
			if view.Code != 302 || !strings.HasPrefix(view.Header().Get("Location"), "/dashboard/mfa") {
				t.Fatalf("required policy lost its pending gate: %d %s", view.Code, view.Header().Get("Location"))
			}
		} else if view.Code != 200 {
			t.Fatalf("old optional pending cookie is trapped after reset: %d %s", view.Code, view.Body)
		}
	}
}

func TestCustomerMFAExpiredBrowserChallengeRefreshesCompletedSession(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, secret, cookie := e.GenerateEnrolledAccount(t)
	h := *e.H()
	challenge := customerMFARequest(t, h, cookie, "GET", "/dashboard/mfa?next=/dashboard/account", nil)
	if challenge.Code != 200 {
		t.Fatalf("completed-session challenge = %d", challenge.Code)
	}
	var csrf *http.Cookie
	for _, c := range challenge.Result().Cookies() {
		if c.Name == "faas_csrf_mfa" {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing MFA CSRF cookie")
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"code": {code}, "csrf_token": {csrf.Value}, "next": {"/dashboard/account"}}
	req := httptest.NewRequest("POST", "/dashboard/mfa", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 303 || rec.Header().Get("Location") != "/dashboard/account" {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body)
	}
}

func TestCustomerMFAExpiredHTMLFormReturnsToConfirmation(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, _, fresh := e.GenerateEnrolledAccount(t)
	mgr := *(*e.Srv()).Sessions()
	env, err := mgr.Verify(fresh.Value)
	if err != nil {
		t.Fatal(err)
	}
	value, err := mgr.IssueWithSessionAndBindingHashAndStepUp(env.Sid, env.AccountID, env.BindingHash, time.Now().Add(-time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/dashboard/account/delete", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: value})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	want := "/dashboard/mfa?next=" + url.QueryEscape("/dashboard/account/delete")
	if rec.Code != 303 || rec.Header().Get("Location") != want {
		t.Fatalf("expired HTML form = %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
}

func TestCustomerMFAPostgresOverageUsesSameSessionPolicy(t *testing.T) {
	for _, enrolled := range []bool{false, true} {
		e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
		if err := (*e.Store()).MarkAccountEmailVerified(t.Context(), e.Acct().ID); err != nil {
			t.Fatal(err)
		}
		cookie := *e.Cookie()
		if enrolled {
			_, _, fresh := e.GenerateEnrolledAccount(t)
			env, _ := (*(*e.Srv()).Sessions()).Verify(fresh.Value)
			value, err := (*(*e.Srv()).Sessions()).IssueWithSessionAndBindingHashAndStepUp(env.Sid, env.AccountID, env.BindingHash, time.Now().Add(-time.Hour), false)
			if err != nil {
				t.Fatal(err)
			}
			cookie = &http.Cookie{Name: apidserver.SessionCookie, Value: value}
		}
		svc, _, _ := resizeAPIService(t, e.Acct().ID)
		*(*e.Srv()).ManagedPostgres() = svc
		csrf, err := middleware.IssueForAuthenticatedNamed(*(*e.Srv()).Sessions(), "postgres_overage", e.Acct().ID, apidserver.DashboardPostgresOverageCSRFCookie)
		if err != nil {
			t.Fatal(err)
		}
		form := url.Values{"enabled": {"off"}, "csrf_token": {csrf}}
		req := httptest.NewRequest("POST", "/dashboard/postgres/overage", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: apidserver.DashboardPostgresOverageCSRFCookie, Value: csrf})
		dashboard := httptest.NewRecorder()
		(*e.H()).ServeHTTP(dashboard, req)
		apiResult := customerMFARequest(t, *e.H(), cookie, "PUT", "/v1/postgres/overage", map[string]bool{"enabled": false})
		if enrolled {
			apidserver.AssertProblem(t, dashboard, 403, api.CodeStepUpRequired)
			apidserver.AssertProblem(t, apiResult, 403, api.CodeStepUpRequired)
		} else if dashboard.Code != 303 || apiResult.Code != 200 {
			t.Fatalf("optional MFA: dashboard=%d API=%d %s", dashboard.Code, apiResult.Code, apiResult.Body)
		}
	}
}
