package apidserver_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func manifestRules(t *testing.T, store *state.MemStore, appID string) []state.EdgeRule {
	t.Helper()
	rules, err := store.ListEdgeRulesForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ManifestKey < rules[j].ManifestKey })
	return rules
}

func TestManifestPathRoutesCreateUpdateAndClear(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	route := gregalemanifest.PathRoute{Name: "reports", MatchHost: "Shop.Example.com", MatchPath: "/reports/**", To: "reports", StripPrefix: "/reports"}
	if _, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, app, []gregalemanifest.PathRoute{route}); problem != nil {
		t.Fatalf("create: %s", problem.Detail)
	}
	rules := manifestRules(t, store, app.ID)
	if len(rules) != 2 {
		t.Fatalf("rules = %+v, want route and rewrite", rules)
	}
	routeRule, rewriteRule := rules[0], rules[1]
	if routeRule.ManifestKey != apidserver.ManifestPathRouteKeyPrefix+"reports" || routeRule.Kind != state.EdgeRuleKindRoute ||
		routeRule.Action.Route == nil || routeRule.Action.Route.TargetAppSlug != "reports" || routeRule.MatchHost != "shop.example.com" || len(routeRule.MatchMethods) != 0 {
		t.Fatalf("route rule = %+v", routeRule)
	}
	if rewriteRule.ManifestKey != apidserver.ManifestPathRouteKeyPrefix+"reports:strip" || rewriteRule.Kind != state.EdgeRuleKindRewrite ||
		rewriteRule.Action.Rewrite == nil || *rewriteRule.Action.Rewrite != (state.EdgeRuleRewriteAction{From: "/reports", To: "/"}) {
		t.Fatalf("rewrite rule = %+v", rewriteRule)
	}

	// The same declaration again changes nothing.
	if _, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, app, []gregalemanifest.PathRoute{route}); problem != nil {
		t.Fatalf("reapply: %s", problem.Detail)
	}
	if again := manifestRules(t, store, app.ID); again[0].ID != routeRule.ID || again[1].ID != rewriteRule.ID || again[0].UpdatedAt != routeRule.UpdatedAt {
		t.Fatalf("idempotent reapply changed rules: %+v", again)
	}

	// Changing the path updates in place; dropping strip_prefix removes the rewrite.
	changed := route
	changed.MatchPath, changed.StripPrefix = "/v2/reports/**", ""
	rollback, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, app, []gregalemanifest.PathRoute{changed})
	if problem != nil {
		t.Fatalf("update: %s", problem.Detail)
	}
	if updated := manifestRules(t, store, app.ID); len(updated) != 1 || updated[0].ID != routeRule.ID || updated[0].MatchPath != "/v2/reports/**" {
		t.Fatalf("updated = %+v", updated)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if restored := manifestRules(t, store, app.ID); len(restored) != 2 || restored[0].MatchPath != "/reports/**" || restored[1].Kind != state.EdgeRuleKindRewrite {
		t.Fatalf("rollback = %+v", restored)
	}

	// A list naming only other apps leaves these routes; an explicit empty list clears them.
	if _, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, app, []gregalemanifest.PathRoute{{Name: "web", MatchHost: "shop.example.com", MatchPath: "/**", To: "web"}}); problem != nil {
		t.Fatalf("other app: %s", problem.Detail)
	}
	if kept := manifestRules(t, store, app.ID); len(kept) != 2 {
		t.Fatalf("routes for another app touched this one: %+v", kept)
	}
	if _, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, app, []gregalemanifest.PathRoute{}); problem != nil {
		t.Fatalf("clear: %s", problem.Detail)
	}
	if cleared := manifestRules(t, store, app.ID); len(cleared) != 0 {
		t.Fatalf("cleared = %+v", cleared)
	}
}

func TestManifestPathRoutesNeverAdoptUnmanagedRules(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	throttle, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "shop.example.com", MatchPath: "/reports/**", Priority: 100, Enabled: true,
		Kind: state.EdgeRuleKindThrottle, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindThrottle, Throttle: &state.EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := gregalemanifest.PathRoute{Name: "reports", MatchHost: "shop.example.com", MatchPath: "/reports/**", To: "reports"}
	if _, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, app, []gregalemanifest.PathRoute{route}); problem != nil {
		t.Fatalf("a throttle on the same path is not a collision: %s", problem.Detail)
	}
	if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "shop.example.com", MatchPath: "/legacy/**", Priority: 100, Enabled: true,
		Kind: state.EdgeRuleKindRoute, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: "reports"}},
	}); err != nil {
		t.Fatal(err)
	}
	legacy := gregalemanifest.PathRoute{Name: "legacy", MatchHost: "shop.example.com", MatchPath: "/legacy/**", To: "reports"}
	_, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, app, []gregalemanifest.PathRoute{route, legacy})
	if problem == nil || problem.Code != api.CodeEdgeRuleConflict || !strings.Contains(problem.Detail, `route "legacy"`) {
		t.Fatalf("problem = %+v, want edge_rule_conflict naming the route", problem)
	}
	for _, rule := range manifestRules(t, store, app.ID) {
		if rule.ID == throttle.ID && rule.ManifestKey != "" {
			t.Fatalf("unmanaged throttle was adopted: %+v", rule)
		}
	}
}

func TestManifestPathRoutesRefuseNonRequestTargets(t *testing.T) {
	srv, store, acct, _ := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	worker, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "jobs", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	_, problem := srv.ApplyManifestPathRoutesForTest(ctx, acct, worker, []gregalemanifest.PathRoute{{Name: "jobs", MatchHost: "shop.example.com", MatchPath: "/jobs/**", To: "jobs"}})
	if problem == nil {
		t.Fatal("a route to a worker must be refused")
	}
}

func TestProjectPathRouteHelpers(t *testing.T) {
	routes := []gregalemanifest.PathRoute{
		{Name: "api", MatchHost: "shop.example.com", MatchPath: "/api/**", To: "backend"},
		{Name: "web", MatchHost: "shop.example.com", MatchPath: "/**", To: "web"},
	}
	app := state.App{Slug: "shop-backend", WorkloadName: "backend", RootDir: "backend"}
	got := apidserver.ProjectPathRoutesForApp(routes, app)
	if len(got) != 1 || got[0].To != "shop-backend" || got[0].Name != "api" {
		t.Fatalf("routes for backend = %+v", got)
	}
	if none := apidserver.ProjectPathRoutesForApp(routes, state.App{Slug: "admin"}); none == nil || len(none) != 0 {
		t.Fatalf("an unrouted app must get an empty, non-nil list: %#v", none)
	}
	if w := apidserver.ProjectPathRoutePlanWarning(routes, true, false); !strings.Contains(w, "2 route declaration(s)") {
		t.Fatalf("warning = %q", w)
	}
	if w := apidserver.ProjectPathRoutePlanWarning(nil, false, false); w != "" {
		t.Fatalf("absent routes warned: %q", w)
	}
	workloads := []reposcan.Workload{{Name: "backend", RootDir: "backend", Class: reposcan.ClassHTTP}, {Name: "web", RootDir: "web", Class: reposcan.ClassHTTP}}
	if problem := apidserver.ValidateProjectManifestPathRoutes(routes, workloads, workloads, nil, nil); problem != nil {
		t.Fatalf("valid project routes: %s", problem.Detail)
	}
	missing := append(routes, gregalemanifest.PathRoute{Name: "x", MatchHost: "shop.example.com", MatchPath: "/x/**", To: "ghost"})
	if problem := apidserver.ValidateProjectManifestPathRoutes(missing, workloads, workloads, nil, nil); problem == nil || !strings.Contains(problem.Detail, `"ghost"`) {
		t.Fatalf("missing target: %+v", problem)
	}
	jobs := []reposcan.Workload{{Name: "backend", RootDir: "backend", Class: reposcan.ClassWorker}, {Name: "web", RootDir: "web", Class: reposcan.ClassHTTP}}
	if problem := apidserver.ValidateProjectManifestPathRoutes(routes, jobs, jobs, nil, nil); problem == nil {
		t.Fatal("a route to a worker workload must be refused")
	}
}
