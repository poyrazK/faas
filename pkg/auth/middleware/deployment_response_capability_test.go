package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	basemw "github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppAgentResponseCheckCapabilityRequiresDeployScope(t *testing.T) {
	const capabilityPath = "/v1/deployments/response-check/capabilities"
	for _, tc := range []struct {
		name, method, path string
		scopes             []string
		status             int
	}{
		{"agent writer", http.MethodGet, capabilityPath, []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}, http.StatusNoContent},
		{"agent reader", http.MethodGet, capabilityPath, []string{api.ScopeAgent, api.ScopeAppsRead}, http.StatusForbidden},
		{"ordinary deploy token", http.MethodGet, capabilityPath, []string{api.ScopeDeployWrite}, http.StatusForbidden},
		{"non-read method", http.MethodPost, capabilityPath, []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}, http.StatusForbidden},
		{"account inventory", http.MethodGet, "/v1/apps", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			account, err := store.CreateAccount(t.Context(), "response-agent@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "response-agent", Type: state.AppTypeApp, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			token, hash, _ := api.GenerateDeployToken()
			if _, err := store.CreateDeployToken(t.Context(), account.ID, app.ID, hash, "response agent", tc.scopes, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			middleware := authmw.New(store, nil, nil, nil, nil, basemw.NewLimiter(basemw.AuthLimitConfig{}), nil)
			handler := middleware.RequireSession(middleware.RequireScope(api.ScopesDeployWriteSurface...)(func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}
