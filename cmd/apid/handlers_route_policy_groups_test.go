package main

// ADR-446: captured route-group planning, impact and transactional verification.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRoutePolicyGroupServerPlanApplyAndStaleCapture(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "group-policy")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:groups", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte(`{"openapi":"3.1.0","paths":{"/checkout/{id}":{"post":{}},"/health":{"get":{}}}}`)
	if err := e.store.UpsertDeploymentOpenAPIDoc(t.Context(), deployment.ID, app.AccountID, app.ID, doc, "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	config, err := routerequirements.ParsePreview([]byte(`{"version":2,"groups":[{"name":"checkout","path_prefix":"/checkout/","methods":["POST"],"require":{"throttle":{"key_by":"none","max_rps":1},"budget":{"explicit":true,"max_ms":500}}}],"public":[{"method":"GET","path":"/health","reason":"secret-private-rationale"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request := api.RoutePolicyPlanRequest{Requirements: config, DeploymentID: deployment.ID, ThrottleBurst: 10}
	plan := routePolicyServerPlan(t, e, slug, request)
	if plan.Version != 3 || plan.Status != "ready" || plan.Before.Coverage.Deployment != deployment.ID || len(plan.Changes) != 2 {
		t.Fatalf("group plan: %+v", plan)
	}
	body, _ := json.Marshal(plan)
	if strings.Contains(string(body), "secret-private-rationale") {
		t.Fatal("rationale escaped into artifact")
	}
	missing := request
	missing.DeploymentID = ""
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/plan", missing, nil); rec.Code != 400 {
		t.Fatalf("deployment missing: %d %s", rec.Code, rec.Body)
	}
	other := mustSeedEdgeRuleApp(t, e, "group-other")
	if rec := e.do(t, "POST", "/v1/apps/"+other+"/route-policy/plan", request, nil); rec.Code != 404 {
		t.Fatal("foreign app capture accepted")
	}
	// Same operation inventory but changed captured bytes must invalidate review.
	if err := e.store.UpsertDeploymentOpenAPIDoc(t.Context(), deployment.ID, app.AccountID, app.ID, append(doc, ' '), "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	apply := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, ExpectedPlanSHA256: plan.SHA256, Confirm: true}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "stale-group"}); rec.Code != 409 {
		t.Fatalf("stale capture accepted: %d %s", rec.Code, rec.Body)
	}
	plan = routePolicyServerPlan(t, e, slug, request)
	apply.ExpectedPlanSHA256 = plan.SHA256
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "reviewed-group"})
	if rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var response api.RoutePolicyApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Receipt.Verification.Status != "satisfied" || len(response.Receipt.Verification.Assignments) != 2 || response.Receipt.Verification.Coverage.SHA256 != plan.Before.Coverage.SHA256 {
		t.Fatalf("verification: %+v", response)
	}
}

func TestRoutePolicyGroupPlanRespectsCapturedContractEntitlement(t *testing.T) {
	inventory := routePolicyInventory(&state.RoutePolicyContract{DeploymentID: "private-capture", SHA256: strings.Repeat("a", 64), Doc: []byte(`{"openapi":"3.1.0","paths":{"/private":{"get":{}}}}`)}, api.PlanFree)
	if inventory.Status != "unavailable" || inventory.Code != "captured_contract_plan_unavailable" || len(inventory.Routes) != 0 || inventory.SHA256 != "" {
		t.Fatalf("capture entitlement bypassed: %+v", inventory)
	}
}
