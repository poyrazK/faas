package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHasScopeUsesAuthenticatedPolicy(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/app/bindings", nil)
	if HasScope(r, api.ScopeAppsRead) || HasScope(r) {
		t.Fatal("missing principal passed scope check")
	}
	r = r.WithContext(WithPrincipal(r.Context(), state.Account{ID: "account"}, &state.APIKey{Scopes: []string{api.ScopeAppsRead}}, nil))
	if !HasScope(r, api.ScopeAppsRead) || HasScope(r, api.ScopeStorageManage) {
		t.Fatal("key scope boundary failed")
	}
	r = r.WithContext(WithPrincipal(r.Context(), state.Account{ID: "account"}, nil, nil))
	if !HasScope(r, api.ScopeStorageManage) {
		t.Fatal("authenticated session did not retain existing permissions")
	}
}
