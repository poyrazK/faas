// adr: 20261121170000
package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/routesplit"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261121170000 amendment 5: the edge-rule API refuses a change that
// would undo a live route split.
func TestHandlers_EdgeRuleChangesRespectALiveRouteSplit(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "split-guard@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "guard-shop", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	split, err := store.CreateRouteSplit(ctx, state.RouteSplit{
		AccountID: acct.ID, SourceAppID: source.ID, TargetSlug: "guard-shop-exports", Kind: "memory",
		Route: "GET /exports/{id}", Method: "GET", PathGlob: "/exports/*", RouteRAMMB: 1024, MainRAMMB: 256, PreviousRAMMB: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := store.CreateEdgeRuleIfUnderQuota(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: source.ID, Name: routesplit.RuleName(split.ID), MatchHost: "guard-shop.example.com",
		MatchPath: "/exports/*", Priority: 100, Enabled: true, Kind: state.EdgeRuleKindIP,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"0.0.0.0/0"}}},
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "split-guard", []string{api.ScopeAppsRead, api.ScopeDeployWrite}); err != nil {
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
	const ipRule = `{"kind":"ip","match_host":%q,"match_path":"/admin/*","action":{"deny":["203.0.113.0/24"]}}`
	for _, tc := range []struct {
		name, method, path, body string
		refused                  bool
	}{
		{"delete the split's rule", http.MethodDelete, "/v1/edge-rules/" + owned.ID, "", true},
		{"disable the split's rule", http.MethodPatch, "/v1/edge-rules/" + owned.ID, `{"enabled":false}`, true},
		{"app-wide rule on the source", http.MethodPost, "/v1/apps/guard-shop/edge-rules", strings.Replace(ipRule, "%q", `"@app"`, 1), true},
		{"host rule on the source", http.MethodPost, "/v1/apps/guard-shop/edge-rules", strings.Replace(ipRule, "%q", `"guard-shop.example.com"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(tc.method, tc.path, tc.body)
			refused := rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), api.CodeRouteSplitRuleConflict)
			if refused != tc.refused {
				t.Fatalf("%s %s = %d %s; want refused=%v", tc.method, tc.path, rec.Code, rec.Body, tc.refused)
			}
		})
	}
}
