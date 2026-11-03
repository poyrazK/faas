package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// companyDeploy runs apply inside a deploy request for app that went through
// appaccess.Wrap, as the route table does, with the request context and the
// account the handler acts as. The organization access fence holds only
// while the request is served, so apply runs inside it.
func companyDeploy(t *testing.T, store *state.MemStore, actor state.Account, app state.App, apply func(context.Context, state.Account)) {
	t.Helper()
	served := false
	mux := http.NewServeMux()
	handler := appaccess.Wrap(store, nil, func(_ http.ResponseWriter, r *http.Request, acct state.Account) {
		served = true
		apply(r.Context(), acct)
	})
	mux.HandleFunc("POST /v1/apps/{slug}/deployments", func(w http.ResponseWriter, r *http.Request) { handler(w, r, actor) })
	rec := httptest.NewRecorder()
	session := middleware.WithPrincipal(t.Context(), actor, nil, nil)
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(session, http.MethodPost, "/v1/apps/"+app.Slug+"/deployments", nil))
	if !served {
		t.Fatalf("deploy was not authorized: %d %s", rec.Code, rec.Body)
	}
}

// A manifest that changes a company app's routes needs the role the
// edge-rules API needs (owner or admin); deploy rights alone redeploy an
// unchanged manifest but cannot add, change or remove its routes.
func TestManifestPathRoutesOnCompanyAppNeedSecurityRole(t *testing.T) {
	srv, store, creator, _ := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "acme-api", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	route := []gregalemanifest.PathRoute{{Name: "api", MatchHost: "shop.example.com", MatchPath: "/api/**", To: app.Slug}}
	deploy := func(routes []gregalemanifest.PathRoute) *api.Problem {
		var problem *api.Problem
		companyDeploy(t, store, creator, app, func(ctx context.Context, owner state.Account) {
			_, problem = srv.ApplyManifestPathRoutesForTest(ctx, owner, app, routes)
		})
		return problem
	}
	setRole := func(role state.OrgRole) {
		if err := store.UpdateOrgMemberRole(ctx, org.ID, creator.ID, role); err != nil {
			t.Fatal(err)
		}
	}

	if problem := deploy(route); problem == nil || problem.Status != http.StatusForbidden || problem.Code != api.CodeOrgRoleForbidden {
		t.Fatalf("developer adding a route: problem = %+v, want 403 %s", problem, api.CodeOrgRoleForbidden)
	}
	if rules := manifestRules(t, store, app.ID); len(rules) != 0 {
		t.Fatalf("refused deploy created rules: %+v", rules)
	}

	setRole(state.OrgRoleAdmin)
	if problem := deploy(route); problem != nil {
		t.Fatalf("admin adding a route: %s", problem.Detail)
	}
	if rules := manifestRules(t, store, app.ID); len(rules) != 1 || rules[0].Kind != state.EdgeRuleKindRoute {
		t.Fatalf("admin rules = %+v, want one route", rules)
	}

	setRole(state.OrgRoleDeveloper)
	if problem := deploy(route); problem != nil {
		t.Fatalf("developer redeploying unchanged routes: %s", problem.Detail)
	}
	if problem := deploy([]gregalemanifest.PathRoute{}); problem == nil || problem.Code != api.CodeOrgRoleForbidden {
		t.Fatalf("developer clearing routes: problem = %+v, want 403 %s", problem, api.CodeOrgRoleForbidden)
	}
	if rules := manifestRules(t, store, app.ID); len(rules) != 1 {
		t.Fatalf("refused clear removed rules: %+v", rules)
	}
}
