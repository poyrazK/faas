package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSelectedProjectManifestAppsRespectsWorkloadSelection(t *testing.T) {
	workloads := []reposcan.Workload{
		{Name: "reports", RootDir: "services/reports"},
		{Name: "billing", RootDir: "services/billing"},
	}
	apps := []state.App{
		{ID: "reports-id", Slug: "reports", WorkloadName: "reports", RootDir: "services/reports"},
		{ID: "billing-id", Slug: "billing-api", WorkloadName: "billing", RootDir: "services/billing"},
	}

	got := selectedProjectManifestApps(workloads[:1], apps)
	if len(got) != 1 || got[0].ID != "reports-id" {
		t.Fatalf("selected apps = %+v; want only reports", got)
	}
}

func TestValidateProjectManifestAsyncRoutesTargetsAndSelection(t *testing.T) {
	routes := []gregalemanifest.AsyncRoute{{App: "worker", Name: "ingest", MatchHost: "api.example.com", MatchPath: "/ingest"}}
	workloads := []reposcan.Workload{
		{Name: "reports", Class: reposcan.ClassHTTP},
		{Name: "worker", Class: reposcan.ClassWorker},
	}

	if problem := validateProjectManifestAsyncRoutes(routes, workloads, workloads[:1], nil, nil); problem != nil {
		t.Fatalf("unselected worker route should remain dormant: %+v", problem)
	}
	if problem := validateProjectManifestAsyncRoutes(routes, workloads, workloads, nil, nil); problem == nil {
		t.Fatal("selected worker route was accepted, want request-invocation class error")
	}
	if problem := validateProjectManifestAsyncRoutes([]gregalemanifest.AsyncRoute{{App: "missing", Name: "x"}}, workloads, workloads, nil, nil); problem == nil {
		t.Fatal("unknown route target was accepted")
	}

	staleApp := state.App{Slug: "retained-worker", WorkloadName: "old-worker", RootDir: "services/old-worker"}
	staleRoute := []gregalemanifest.AsyncRoute{{App: "retained-worker", Name: "x"}}
	if problem := validateProjectManifestAsyncRoutes(staleRoute, workloads, workloads, []state.App{staleApp}, nil); problem == nil {
		t.Fatal("stale route target without --exclude was accepted")
	}
	if problem := validateProjectManifestAsyncRoutes(staleRoute, workloads, workloads, []state.App{staleApp}, map[string]bool{"retained-worker": true}); problem != nil {
		t.Fatalf("explicitly excluded stale route target: %+v", problem)
	}
	if problem := validateProjectManifestAsyncRoutes(staleRoute, workloads, workloads, []state.App{staleApp}, map[string]bool{"services/old-worker": true}); problem != nil {
		t.Fatalf("stale route target excluded by root dir: %+v", problem)
	}
}

func TestApplyProjectManifestAsyncRoutesScopesClearsAndNoTriggers(t *testing.T) {
	srv, store, acct, reports := sourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	billing, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "billing", Type: state.AppTypeApp,
		ProjectID: "project-id", WorkloadName: "billing", RootDir: "services/billing",
	})
	if err != nil {
		t.Fatal(err)
	}
	reports.ProjectID = "project-id"
	reports.WorkloadName = "reports"
	reports.RootDir = "services/reports"
	routes := []gregalemanifest.AsyncRoute{
		{App: "reports", Name: "create-report", MatchHost: "reports.example.com", MatchPath: "/reports"},
		{App: "billing", Name: "create-invoice", MatchHost: "billing.example.com", MatchPath: "/invoices"},
	}

	if problem := srv.applyProjectManifestAsyncRoutes(ctx, acct, routes, true, false, []state.App{billing, reports}); problem != nil {
		t.Fatalf("apply project routes: %+v", problem)
	}
	assertRuleCount := func(app state.App, want int) {
		t.Helper()
		rules, err := store.ListEdgeRulesForApp(ctx, app.ID)
		if err != nil || len(rules) != want {
			t.Fatalf("%s route count = %d, err=%v; want %d", app.Slug, len(rules), err, want)
		}
	}
	assertRuleCount(reports, 1)
	assertRuleCount(billing, 1)

	// Removing one app's declaration clears that app's manifest-owned rules
	// when it is selected, while the remaining declaration is reconciled.
	reportsOnly := routes[:1]
	if problem := srv.applyProjectManifestAsyncRoutes(ctx, acct, reportsOnly, true, false, []state.App{reports, billing}); problem != nil {
		t.Fatalf("remove omitted selected workload route: %+v", problem)
	}
	assertRuleCount(reports, 1)
	assertRuleCount(billing, 0)
	if problem := srv.applyProjectManifestAsyncRoutes(ctx, acct, routes, true, false, []state.App{billing}); problem != nil {
		t.Fatalf("reapply billing route: %+v", problem)
	}
	assertRuleCount(billing, 1)

	// An explicit empty declaration clears only selected workloads. A
	// workload excluded from this apply keeps its existing route.
	if problem := srv.applyProjectManifestAsyncRoutes(ctx, acct, []gregalemanifest.AsyncRoute{}, true, false, []state.App{reports}); problem != nil {
		t.Fatalf("clear selected project routes: %+v", problem)
	}
	assertRuleCount(reports, 0)
	assertRuleCount(billing, 1)

	// --no-triggers and an omitted manifest key both preserve current state.
	if problem := srv.applyProjectManifestAsyncRoutes(ctx, acct, []gregalemanifest.AsyncRoute{}, true, true, []state.App{billing}); problem != nil {
		t.Fatalf("no-triggers project apply: %+v", problem)
	}
	if problem := srv.applyProjectManifestAsyncRoutes(ctx, acct, nil, false, false, []state.App{billing}); problem != nil {
		t.Fatalf("omitted async_routes project apply: %+v", problem)
	}
	assertRuleCount(billing, 1)
}
