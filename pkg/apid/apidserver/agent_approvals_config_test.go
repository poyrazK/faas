package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261104140058 amendment 2026-11-10 (apps/projects review P10): an agent
// key's production configuration writes wait for a person like a release; a
// stage-scope write or a preview app's configuration does not.
func TestHandlers_AgentProductionConfigWritesNeedApproval(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "config-approver@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{AccountID: acct.ID, Slug: "cfg-prod", Type: state.AppTypeApp},
		{AccountID: acct.ID, Slug: "cfg-prod-pr-1", Type: state.AppTypeApp, PreviewOfSlug: "cfg-prod", PreviewPrNumber: 1},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %s: %v", app.Slug, err)
		}
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "config-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeEnvWrite}); err != nil {
		t.Fatal(err)
	}
	const env = `{"value":"debug"}`
	for _, tc := range []struct {
		name, method, path, body string
		gated                    bool
	}{
		{"production env", http.MethodPut, "/v1/apps/cfg-prod/env/LOG_LEVEL", env, true},
		{"named production env", http.MethodPut, "/v1/apps/cfg-prod/env/LOG_LEVEL?scope=production", env, true},
		{"production env batch", http.MethodPost, "/v1/apps/cfg-prod/env/batch", `{"set":{"LOG_LEVEL":"debug"}}`, true},
		{"stage env", http.MethodPut, "/v1/apps/cfg-prod/env/LOG_LEVEL?scope=staging", env, false},
		{"preview env", http.MethodPut, "/v1/apps/cfg-prod-pr-1/env/LOG_LEVEL", env, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+plaintext)
			r.Header.Set("Content-Type", "application/json")
			srv.ServeHTTP(rec, r)
			if gated := rec.Code == http.StatusPreconditionRequired; gated != tc.gated {
				t.Fatalf("%s %s = %d %s; want gated=%v", tc.method, tc.path, rec.Code, rec.Body, tc.gated)
			}
		})
	}
}
