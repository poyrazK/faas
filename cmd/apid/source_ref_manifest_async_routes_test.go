package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func sourceRefAsyncRouteTestFixture(t *testing.T, plans ...api.Plan) (*server, *state.MemStore, state.Account, state.App) {
	t.Helper()
	ctx := context.Background()
	plan := api.PlanPro
	if len(plans) > 0 {
		plan = plans[0]
	}
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "async-manifest@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "reports", Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newServer(store, slog.Default(), "gregale.dev", noopNotifier{}), store, acct, app
}

func TestApplySourceRefManifestAsyncRoutesGatesPlan(t *testing.T) {
	srv, store, acct, app := sourceRefAsyncRouteTestFixture(t, api.PlanFree)
	staged := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
	problem := srv.applySourceRefManifestAsyncRoutes(context.Background(), acct, app, []gregalemanifest.AsyncRoute{{
		App: "reports", Name: "create-report", MatchHost: "reports.example.com", MatchPath: "/reports",
	}}, &staged)
	if problem == nil || problem.Code != api.CodePlanEdgeRuleKindNotAllowed {
		t.Fatalf("Free plan problem = %+v, want async edge-rule plan gate", problem)
	}
	rules, err := store.ListEdgeRulesForApp(context.Background(), app.ID)
	if err != nil || len(rules) != 0 {
		t.Fatalf("rules after rejected plan = %+v, err=%v; want none", rules, err)
	}
}

func TestApplySourceRefManifestAsyncRoutesValidatesWebhookOwnership(t *testing.T) {
	srv, store, acct, app := sourceRefAsyncRouteTestFixture(t)
	staged := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
	problem := srv.applySourceRefManifestAsyncRoutes(context.Background(), acct, app, []gregalemanifest.AsyncRoute{{
		App: "reports", Name: "create-report", MatchHost: "reports.example.com", MatchPath: "/reports",
		OnSuccess: "not-an-app-webhook",
	}}, &staged)
	if problem == nil || !strings.Contains(problem.Detail, "webhook owned by this app") {
		t.Fatalf("destination problem = %+v, want app-owned webhook validation", problem)
	}
	rules, err := store.ListEdgeRulesForApp(context.Background(), app.ID)
	if err != nil || len(rules) != 0 {
		t.Fatalf("rules after invalid destination = %+v, err=%v; want none", rules, err)
	}
}

func TestApplySourceRefManifestSkipsAsyncRoutesWhenNoTriggers(t *testing.T) {
	srv, store, acct, app := sourceRefAsyncRouteTestFixture(t, api.PlanFree)
	manifest := &gregalemanifest.Manifest{AsyncRoutes: []gregalemanifest.AsyncRoute{{
		App: "reports", Name: "create-report", MatchHost: "reports.example.com", MatchPath: "/reports",
	}}}
	staged, problem := srv.applySourceRefManifest(context.Background(), acct, app, manifest, "", false)
	if problem != nil {
		t.Fatalf("apply with no_triggers: %s", problem.Detail)
	}
	if sourceRefManifestNeedsRollback(staged) {
		t.Fatalf("staged changes = %+v, want none", staged)
	}
	rules, err := store.ListEdgeRulesForApp(context.Background(), app.ID)
	if err != nil || len(rules) != 0 {
		t.Fatalf("rules after no_triggers = %+v, err=%v; want none", rules, err)
	}
}

func TestApplySourceRefManifestAsyncRoutesReconcilesByName(t *testing.T) {
	srv, store, acct, app := sourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	first := gregalemanifest.AsyncRoute{
		App: "reports", Name: "create-report", MatchHost: "reports.example.com", MatchPath: "/reports",
	}
	staged := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
	if problem := srv.applySourceRefManifestAsyncRoutes(ctx, acct, app, []gregalemanifest.AsyncRoute{first}, &staged); problem != nil {
		t.Fatalf("create route: %s", problem.Detail)
	}
	rules, err := store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules after create = %d, err=%v; want one", len(rules), err)
	}
	created := rules[0]
	if created.ManifestKey != manifestAsyncRouteKeyPrefix+first.Name || created.Kind != state.EdgeRuleKindAsync || created.MatchMethods[0] != "POST" {
		t.Fatalf("created rule = %+v", created)
	}

	changed := first
	changed.MatchPath = "/reports/v2"
	updateStage := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
	if problem := srv.applySourceRefManifestAsyncRoutes(ctx, acct, app, []gregalemanifest.AsyncRoute{changed}, &updateStage); problem != nil {
		t.Fatalf("update route: %s", problem.Detail)
	}
	rules, err = store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules after update = %d, err=%v; want one", len(rules), err)
	}
	if rules[0].ID != created.ID || rules[0].MatchPath != "/reports/v2" {
		t.Fatalf("updated route = %+v; want same ID and new path", rules[0])
	}
	if err := srv.rollbackSourceRefManifest(ctx, updateStage); err != nil {
		t.Fatalf("rollback update: %v", err)
	}
	rules, err = store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 1 || rules[0].MatchPath != "/reports" {
		t.Fatalf("rules after update rollback = %+v, err=%v; want original path", rules, err)
	}

	deleteStage := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
	if problem := srv.applySourceRefManifestAsyncRoutes(ctx, acct, app, []gregalemanifest.AsyncRoute{}, &deleteStage); problem != nil {
		t.Fatalf("clear routes: %s", problem.Detail)
	}
	rules, err = store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 0 {
		t.Fatalf("rules after explicit empty manifest = %+v, err=%v; want empty", rules, err)
	}
	if err := srv.rollbackSourceRefManifest(ctx, deleteStage); err != nil {
		t.Fatalf("rollback delete: %v", err)
	}
	rules, err = store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 1 || rules[0].ManifestKey != manifestAsyncRouteKeyPrefix+first.Name {
		t.Fatalf("rules after delete rollback = %+v, err=%v; want restored manifest route", rules, err)
	}
}

func TestApplySourceRefManifestAsyncRoutesDoesNotAdoptUnmanagedRule(t *testing.T) {
	srv, store, acct, app := sourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	_, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "reports.example.com", MatchPath: "/reports",
		MatchMethods: []string{"POST"}, Priority: 100, Enabled: true, Kind: state.EdgeRuleKindAsync,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindAsync, Async: &state.EdgeRuleAsyncAction{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	staged := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
	problem := srv.applySourceRefManifestAsyncRoutes(ctx, acct, app, []gregalemanifest.AsyncRoute{{
		App: "reports", Name: "create-report", MatchHost: "reports.example.com", MatchPath: "/reports",
	}}, &staged)
	if problem == nil || problem.Code != api.CodeEdgeRuleConflict {
		t.Fatalf("problem = %+v, want edge_rule_conflict", problem)
	}
	rules, err := store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 1 || rules[0].ManifestKey != "" {
		t.Fatalf("unmanaged rule changed: rules=%+v err=%v", rules, err)
	}
}
