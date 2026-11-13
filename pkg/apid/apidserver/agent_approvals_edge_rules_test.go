// adr: 091
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

// ADR-091 amendment 2026-11-07: an agent key's edge-rule writes on a
// production app wait for a person, as its rollbacks already did. A route,
// redirect or maintenance rule moves or stops traffic like a rollback, and a
// gate edit opens or closes the app. A preview app's rules are not gated.
func TestHandlers_AgentEdgeRuleWritesNeedApproval(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "edge-approver@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]state.App{}
	for _, app := range []state.App{
		{AccountID: acct.ID, Slug: "edge-prod", Type: state.AppTypeApp},
		{AccountID: acct.ID, Slug: "edge-prod-pr-1", Type: state.AppTypeApp, PreviewOfSlug: "edge-prod", PreviewPrNumber: 1},
	} {
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatalf("CreateApp %s: %v", app.Slug, err)
		}
		apps[app.Slug] = created
	}
	rule := func(app state.App) string {
		t.Helper()
		row, err := store.CreateEdgeRuleIfUnderQuota(ctx, state.CreateEdgeRuleParams{
			AccountID: acct.ID, AppID: app.ID, MatchHost: "@app", MatchPath: "/", Priority: 100, Enabled: true,
			Kind: state.EdgeRuleKindIP, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"203.0.113.0/24"}}},
		}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatalf("seed rule on %s: %v", app.Slug, err)
		}
		return row.ID
	}
	prodRule, previewRule := rule(apps["edge-prod"]), rule(apps["edge-prod-pr-1"])
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "edge-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+plaintext)
		r.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, r)
		return rec
	}
	const create = `{"kind":"redirect","match_host":"@app","match_path":"/","action":{"to":"https://example.com/","status_code":302}}`
	for _, tc := range []struct {
		name, method, path, body string
		gated                    bool
	}{
		{"create on production", http.MethodPost, "/v1/apps/edge-prod/edge-rules", create, true},
		{"update on production", http.MethodPatch, "/v1/edge-rules/" + prodRule, `{"enabled":false}`, true},
		{"attack mode on production", http.MethodPut, "/v1/apps/edge-prod/edge-protection/attack-mode", `{}`, true},
		{"create on a preview", http.MethodPost, "/v1/apps/edge-prod-pr-1/edge-rules", create, false},
		{"update on a preview", http.MethodPatch, "/v1/edge-rules/" + previewRule, `{"enabled":false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(tc.method, tc.path, tc.body)
			if gated := rec.Code == http.StatusPreconditionRequired; gated != tc.gated {
				t.Fatalf("%s %s = %d %s; want gated=%v", tc.method, tc.path, rec.Code, rec.Body, tc.gated)
			}
		})
	}
}
