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

func TestRequireSession_PlatformTenantAccessTokenIsTenantSelfOnly(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(context.Background(), "mw-platform-tenant-access@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(context.Background(), account.ID, "downstream", "Downstream", 250)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, prefix, hash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlatformTenantAccessToken(context.Background(), state.PlatformTenantAccessTokenInput{
		AccountID: account.ID, TenantID: tenant.ID, Name: "statement reader", Prefix: prefix, TokenHash: hash,
		Scopes: []string{api.ScopePlatformTenantStatementsRead}, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	mw := authmw.New(store, nil, nil, nil, nil, basemw.NewLimiter(basemw.AuthLimitConfig{}), nil)
	accepted := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/platform-tenant-self/usage-statements", nil)
	request.Header.Set("Authorization", "Bearer "+plaintext)
	mw.RequireSession(func(w http.ResponseWriter, r *http.Request, got state.Account) {
		_, key, ok := authmw.AccountFromContext(r)
		if !ok || key == nil || key.PlatformTenantID != tenant.ID || got.ID != account.ID {
			t.Errorf("tenant principal = account %q, key %+v, ok=%v", got.ID, key, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})(accepted, request)
	if accepted.Code != http.StatusNoContent {
		t.Fatalf("tenant self-service status = %d, want 204", accepted.Code)
	}

	for _, tc := range []struct {
		method string
		path   string
	}{{http.MethodGet, "/v1/account"}, {http.MethodPost, "/v1/platform-tenant-self/usage-statements"}} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Header.Set("Authorization", "Bearer "+plaintext)
		mw.RequireSession(func(http.ResponseWriter, *http.Request, state.Account) {
			t.Errorf("tenant token reached forbidden route %s %s", tc.method, tc.path)
		})(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", tc.method, tc.path, recorder.Code)
		}
	}
}
