package main

// ADR-447: opt-in budget synthesis bound to the reviewed plan and transaction.

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRoutePolicyBudgetConsolidationPlanAndApply(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "budget-group")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:budget", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte(`{"openapi":"3.1.0","paths":{"/reports/daily":{"get":{}},"/reports/monthly":{"get":{}}}}`)
	if err := e.store.UpsertDeploymentOpenAPIDoc(t.Context(), deployment.ID, app.AccountID, app.ID, doc, "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	config, err := routerequirements.ParsePreview([]byte(`{"version":2,"groups":[{"name":"reports","path_prefix":"/reports/","methods":["GET"],"require":{"budget":{"explicit":true,"max_ms":500}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request := api.RoutePolicyPlanRequest{Requirements: config, DeploymentID: deployment.ID, ConsolidateBudgets: true}
	plan := routePolicyServerPlan(t, e, slug, request)
	if plan.Status != "ready" || len(plan.Changes) != 1 || plan.Changes[0].BudgetGroup != "reports" || plan.RuleUsage.After != plan.RuleUsage.Before+1 {
		t.Fatalf("consolidated plan: %+v", plan)
	}
	apply := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, ExpectedPlanSHA256: plan.SHA256, Confirm: true}
	apply.ConsolidateBudgets = false
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "changed-option"}); rec.Code != 409 {
		t.Fatalf("changed option applied: %d %s", rec.Code, rec.Body)
	}
	apply.ConsolidateBudgets = true
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "consolidated-budget"})
	if rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var response api.RoutePolicyApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Receipt.Changes) != 1 || response.Receipt.Verification.Status != "satisfied" {
		t.Fatalf("receipt: %+v", response)
	}
}

func TestRoutePolicyBudgetConsolidationRejectsConcreteRequirements(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "budget-invalid")
	config, _, err := routerequirements.Normalize(api.RouteRequirementsConfig{Version: 1, Routes: []api.RouteRequirement{{Method: "GET", Path: "/reports", Require: api.RouteChecks{Budget: &api.RouteBudgetRequirement{Explicit: true}}}}})
	if err != nil {
		t.Fatal(err)
	}
	request := api.RoutePolicyPlanRequest{Requirements: config, ConsolidateBudgets: true}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/plan", request, nil); rec.Code != 400 {
		t.Fatalf("concrete consolidation accepted: %d %s", rec.Code, rec.Body)
	}
}
