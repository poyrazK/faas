package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	basemw "github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// An OIDC bearer exchanged under a trust policy Gregale created is bound to
// one app (ADR-20261101100000) and confined like a per-app deploy token; an
// account-wide bearer from a policy the customer wrote is not.
func TestRequireSession_OIDCBearerBoundToItsApp(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "mw-oidc-bound@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "mw-oidc-app", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "mw-oidc-other", Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	mint := func(appID string) string {
		t.Helper()
		plain, hash, err := api.GenerateOIDCKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.InsertOIDCExchangedToken(ctx, &state.OIDCExchangedToken{AccountID: acct.ID, TokenHash: hash,
			ExpiresAt: time.Now().Add(time.Hour), IssuerURL: "https://token.actions.githubusercontent.com",
			Subject: "repo:octocat/hello:ref:refs/heads/main", Scopes: []string{api.ScopeDeployWrite}, AppID: appID}); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	mw := authmw.New(store, nil, nil, nil, nil, basemw.NewLimiter(basemw.AuthLimitConfig{}), nil)
	call := func(bearer, method, path, slug string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if slug != "" {
			req.SetPathValue("slug", slug)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		mw.RequireSession(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
			w.WriteHeader(http.StatusNoContent)
		})(rec, req)
		return rec.Code
	}

	bound := mint(app.ID)
	if code := call(bound, http.MethodPost, "/v1/apps/mw-oidc-app/deployments/source-ref", "mw-oidc-app"); code != http.StatusNoContent {
		t.Fatalf("own app = %d, want 204", code)
	}
	if code := call(bound, http.MethodPost, "/v1/apps/mw-oidc-other/deployments/source-ref", "mw-oidc-other"); code != http.StatusNotFound {
		t.Fatalf("another app = %d, want 404", code)
	}
	if code := call(bound, http.MethodPost, "/v1/apps", ""); code != http.StatusForbidden {
		t.Fatalf("account route = %d, want 403", code)
	}

	accountWide := mint("")
	if code := call(accountWide, http.MethodPost, "/v1/apps/mw-oidc-other/deployments/source-ref", "mw-oidc-other"); code != http.StatusNoContent {
		t.Fatalf("account-wide bearer on another app = %d, want 204", code)
	}
}
