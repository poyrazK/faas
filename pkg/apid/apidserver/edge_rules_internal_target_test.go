// adr: 091. A route rule cannot put an internal app on the public edge.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private apps are off the public edge entirely, but a same-account
// kind=route rule served an internal app on a public host. The gateway now
// refuses internal targets, and apid refuses to store a rule that could only
// have exposed one.
func TestEdgeRuleRouteRefusesInternalTarget(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	for _, slug := range []string{"front", "private-api", "public-api"} {
		if rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug}, nil); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", slug, rec.Code, rec.Body.String())
		}
	}
	internal := api.AppVisibilityInternal
	if _, err := (*e.Store()).UpdateApp(context.Background(), findAppID(t, e, "private-api"), state.UpdateAppParams{
		Visibility: &internal, SetVisibility: true,
	}); err != nil {
		t.Fatal(err)
	}
	route := func(target string) api.CreateEdgeRuleRequest {
		return api.CreateEdgeRuleRequest{MatchHost: "shop.example.com", MatchPath: "/api/*",
			Kind: string(state.EdgeRuleKindRoute), Action: json.RawMessage(`{"target_app_slug":"` + target + `"}`)}
	}
	refused := e.Do(t, http.MethodPost, "/v1/apps/front/edge-rules", route("private-api"), nil)
	apidserver.AssertProblem(t, refused, http.StatusBadRequest, api.CodeValidation)
	if !strings.Contains(refused.Body.String(), "internal app") {
		t.Fatalf("refusal does not explain the internal target: %s", refused.Body.String())
	}
	created := e.Do(t, http.MethodPost, "/v1/apps/front/edge-rules", route("public-api"), nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("public target: %d %s", created.Code, created.Body.String())
	}
	var rule api.EdgeRuleResponse
	if err := json.Unmarshal(created.Body.Bytes(), &rule); err != nil {
		t.Fatal(err)
	}
	action := json.RawMessage(`{"target_app_slug":"private-api"}`)
	patched := e.Do(t, http.MethodPatch, "/v1/edge-rules/"+rule.ID, api.UpdateEdgeRuleRequest{Action: &action}, nil)
	apidserver.AssertProblem(t, patched, http.StatusBadRequest, api.CodeValidation)
}

// A manifest path route serves its target app on the declared public host.
func TestManifestPathRoutesRefuseInternalTargets(t *testing.T) {
	srv, store, acct, _ := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	private, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "reports-internal", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassHTTP, Visibility: api.AppVisibilityInternal})
	if err != nil {
		t.Fatal(err)
	}
	_, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, private, []gregalemanifest.PathRoute{{Name: "reports", MatchHost: "shop.example.com", MatchPath: "/reports/**", To: "reports-internal"}})
	if problem == nil || !strings.Contains(problem.Detail, "internal app") {
		t.Fatalf("manifest route to an internal app = %+v, want a refusal", problem)
	}
}
