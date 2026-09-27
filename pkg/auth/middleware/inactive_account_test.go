package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
