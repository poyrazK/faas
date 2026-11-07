package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestInactiveAccountMayReach pins the recovery allowlist: a suspended or
// deleted_pending account reaches the routes that end that state and
// nothing else. Every suspended request used to 402, including the
// `gregale billing portal` call its suspension email tells it to make.
func TestInactiveAccountMayReach(t *testing.T) {
	t.Parallel()
	suspended := state.Account{Status: state.AccountSuspended}
	deleting := state.Account{Status: state.AccountDeletedPending}
	cases := []struct {
		acct         state.Account
		method, path string
		want         bool
	}{
		{suspended, "GET", "/v1/billing/portal", true},
		{suspended, "GET", "/v1/billing/status", true},
		{suspended, "POST", "/v1/billing/retry", true},
		{suspended, "PATCH", "/v1/account/plan", true},
		{suspended, "GET", "/v1/account", true},
		{suspended, "GET", "/v1/account/export", true},
		{suspended, "GET", "/v1/usage", true},
		{suspended, "POST", "/v1/account/mfa/verify", true},
		{suspended, "POST", "/v1/account/mfa/recover", true},
		{suspended, "POST", "/v1/account/mfa/enroll", true},
		{suspended, "POST", "/v1/account/mfa/confirm", true},
		{suspended, "POST", "/v1/account/mfa/disable", true},
		{suspended, "POST", "/v1/account/mfa/disable-email", true},
		{suspended, "POST", "/v1/account/mfa/disable-email/confirm", true},
		{suspended, "GET", "/auth/reset/mfa", true},
		{suspended, "GET", "/v1/auth/csrf", true},
		{suspended, "DELETE", "/v1/account", false},
		{suspended, "POST", "/v1/account/restore", false},
		{suspended, "POST", "/v1/billing/cancel", false},
		{suspended, "GET", "/v1/apps", false},
		{suspended, "POST", "/v1/apps/x/deployments", false},
		{deleting, "POST", "/v1/account/restore", true},
		{deleting, "DELETE", "/v1/account", true},
		{deleting, "GET", "/v1/billing/portal", true},
		{deleting, "GET", "/v1/apps", false},
		{state.Account{Status: "unknown"}, "GET", "/v1/billing/portal", false},
	}
	for _, tc := range cases {
		if got := middleware.InactiveAccountMayReach(tc.acct, tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s %s = %v, want %v", tc.acct.Status, tc.method, tc.path, got, tc.want)
		}
	}
}

func TestRequireSession_BearerSuspendedAccountReachesBillingPortal(t *testing.T) {
	authn := newFakeAuthn()
	authn.authKey[string(api.HashAPIKey(validBearerKey))] = authResult{
		acct: state.Account{ID: "acct-1", Email: "x@y", Status: state.AccountSuspended},
		key:  mkKey("key-1"),
	}
	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	h(rec, mkRequest("GET", "/v1/billing/portal", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil))
	if hits != 1 {
		t.Fatalf("hits = %d (status %d), want the billing portal to be reachable while suspended", hits, rec.Code)
	}
}

// H87: a member whose personal account is suspended for billing reaches the
// routes that address an application, where appaccess decides by the app's
// owner (a company app follows the company). Every other route still answers
// 402, and an abuse hold or a pending deletion still locks every route.
func TestRequireSession_BillingSuspendedCallerReachesAppAddressedRoutes(t *testing.T) {
	held := time.Now()
	cases := []struct {
		name    string
		acct    state.Account
		pattern string
		path    string
		want    bool
	}{
		{"company app read", state.Account{ID: "a", Status: state.AccountSuspended}, "GET /v1/apps/{slug}", "/v1/apps/acme-api", true},
		{"company app deploy", state.Account{ID: "a", Status: state.AccountSuspended}, "POST /v1/apps/{slug}/deployments", "/v1/apps/acme-api/deployments", true},
		{"org-scoped app", state.Account{ID: "a", Status: state.AccountSuspended}, "GET /v1/orgs/{organization}/apps/{slug}/logs", "/v1/orgs/acme/apps/acme-api/logs", true},
		{"deployment by id", state.Account{ID: "a", Status: state.AccountSuspended}, "POST /v1/deployments/{id}/retry", "/v1/deployments/d1/retry", true},
		{"create personal app", state.Account{ID: "a", Status: state.AccountSuspended}, "POST /v1/apps", "/v1/apps", false},
		{"list apps", state.Account{ID: "a", Status: state.AccountSuspended}, "GET /v1/apps", "/v1/apps", false},
		{"upload", state.Account{ID: "a", Status: state.AccountSuspended}, "POST /v1/uploads", "/v1/uploads", false},
		{"abuse hold", state.Account{ID: "a", Status: state.AccountActive, AbuseHoldAt: &held}, "GET /v1/apps/{slug}", "/v1/apps/acme-api", false},
		{"suspended and abuse held", state.Account{ID: "a", Status: state.AccountSuspended, AbuseHoldAt: &held}, "GET /v1/apps/{slug}", "/v1/apps/acme-api", false},
		{"deletion pending", state.Account{ID: "a", Status: state.AccountDeletedPending}, "GET /v1/apps/{slug}", "/v1/apps/acme-api", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authn := newFakeAuthn()
			authn.authKey[string(api.HashAPIKey(validBearerKey))] = authResult{acct: tc.acct, key: mkKey("key-1")}
			mw := newMW(t, authn, nil, nil, nil)
			hits := 0
			h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })
			req := mkRequest(strings.Fields(tc.pattern)[0], tc.path, map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
			req.Pattern = tc.pattern
			rec := httptest.NewRecorder()
			h(rec, req)
			if got := hits == 1; got != tc.want {
				t.Fatalf("reached = %v (status %d), want %v", got, rec.Code, tc.want)
			}
			if !tc.want && rec.Code != http.StatusPaymentRequired {
				t.Fatalf("refusal status = %d, want 402", rec.Code)
			}
		})
	}
}
