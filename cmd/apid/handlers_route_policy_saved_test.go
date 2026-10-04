package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSavedRoutePolicyServerPlanApply(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "saved-repair")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:saved-repair", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte(`{"openapi":"3.1.0","paths":{"/checkout/{id}":{"post":{}},"/health":{"get":{}}}}`)
	if err := e.store.UpsertDeploymentOpenAPIDoc(t.Context(), deployment.ID, app.AccountID, app.ID, doc, "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	request := api.RoutePolicyPlanRequest{Saved: true, DeploymentID: deployment.ID}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/plan", request, nil); rec.Code != 404 {
		t.Fatalf("missing saved intent: %d %s", rec.Code, rec.Body)
	}
	max := int64(500)
	config := api.RouteRequirementsConfig{Version: 2, Groups: []api.RouteGroup{{Name: "checkout", PathPrefix: "/checkout/", Methods: []string{"POST"}, Require: api.RouteChecks{Budget: &api.RouteBudgetRequirement{MaxMS: &max, Explicit: true}}}}, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "private commentary"}}}
	zero := int64(0)
	rec := e.do(t, "PUT", "/v1/apps/"+slug+"/route-requirements", api.SaveRouteRequirementsRequest{Requirements: config, ExpectedRevision: &zero}, nil)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	plan := routePolicyServerPlan(t, e, slug, request)
	if plan.RequirementsRevision != 1 || plan.Version != 3 || plan.Status != "ready" || len(plan.Changes) != 1 || plan.After.Status != "satisfied" {
		t.Fatalf("saved plan: %+v", plan)
	}
	if body, _ := json.Marshal(plan); strings.Contains(string(body), "private commentary") {
		t.Fatal("plan exposed saved commentary")
	}
	for _, bad := range []api.RoutePolicyPlanRequest{
		{Saved: true},
		{Saved: true, DeploymentID: deployment.ID, Requirements: config},
		{Saved: true, DeploymentID: deployment.ID, ExpectedRevision: &zero},
		{Requirements: config, DeploymentID: deployment.ID, ExpectedRevision: &plan.RequirementsRevision},
	} {
		if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/plan", bad, nil); rec.Code != 400 {
			t.Fatalf("invalid source accepted: %d %s", rec.Code, rec.Body)
		}
	}
	apply := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, Confirm: true, ExpectedPlanSHA256: plan.SHA256}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "unpinned"}); rec.Code != 400 {
		t.Fatal("unpinned saved apply accepted")
	}
	apply.ExpectedRevision = &plan.RequirementsRevision
	max = 700
	rec = e.do(t, "PUT", "/v1/apps/"+slug+"/route-requirements", api.SaveRouteRequirementsRequest{Requirements: config, ExpectedRevision: &plan.RequirementsRevision}, nil)
	if rec.Code != 200 {
		t.Fatalf("replace intent: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"plan", "apply"} {
		var body any = apply.RoutePolicyPlanRequest
		if path == "apply" {
			body = apply
		}
		if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/"+path, body, map[string]string{"Idempotency-Key": "stale"}); rec.Code != 409 || !strings.Contains(rec.Body.String(), "Saved route requirements changed") {
			t.Fatalf("stale %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	fresh := routePolicyServerPlan(t, e, slug, request)
	if fresh.RequirementsRevision != 2 || fresh.SHA256 == plan.SHA256 {
		t.Fatal("fresh saved plan not revision bound")
	}
	apply.ExpectedRevision, apply.ExpectedPlanSHA256 = &fresh.RequirementsRevision, fresh.SHA256
	rec = e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "repair"})
	var first api.RoutePolicyApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil || rec.Code != 200 || first.Receipt.Verification.Status != "satisfied" {
		t.Fatalf("repair: %d %s %v", rec.Code, rec.Body, err)
	}
	max = 900
	if rec := e.do(t, "PUT", "/v1/apps/"+slug+"/route-requirements", api.SaveRouteRequirementsRequest{Requirements: config, ExpectedRevision: &fresh.RequirementsRevision}, nil); rec.Code != 200 {
		t.Fatal("later intent update failed")
	}
	rec = e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "repair"})
	var replay api.RoutePolicyApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &replay); err != nil || rec.Code != 200 || !replay.Replayed || replay.Receipt.ID != first.Receipt.ID {
		t.Fatalf("recovery: %d %s %v", rec.Code, rec.Body, err)
	}
}
