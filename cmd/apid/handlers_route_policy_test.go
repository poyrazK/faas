package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func routePolicyServerRequest(t *testing.T) api.RoutePolicyPlanRequest {
	t.Helper()
	config, err := routerequirements.Parse([]byte(`{"version":1,"routes":[{"method":"POST","path":"/checkout","require":{"throttle":{"key_by":"none","max_rps":1},"budget":{"max_ms":500}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return api.RoutePolicyPlanRequest{Requirements: config, ThrottleBurst: 10}
}

func routePolicyServerPlan(t *testing.T, e testEnv, slug string, request api.RoutePolicyPlanRequest) api.RoutePolicyPlan {
	t.Helper()
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/plan", request, nil)
	if rec.Code != 200 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	var plan api.RoutePolicyPlan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRoutePolicyServerApplyReceiptAndTampering(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "route-policy")
	request := routePolicyServerRequest(t)
	plan := routePolicyServerPlan(t, e, slug, request)
	if plan.Version != 2 || plan.Status != "ready" || len(plan.Changes) != 2 {
		t.Fatalf("plan=%+v", plan)
	}
	app, _ := e.store.AppBySlug(t.Context(), slug)
	rules, _ := e.store.ListEdgeRulesForApp(t.Context(), app.ID)
	if len(rules) != 0 {
		t.Fatal("plan changed policy")
	}
	apply := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, ExpectedPlanSHA256: plan.SHA256, Confirm: true}
	tampered := apply
	tampered.ThrottleBurst = 11
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", tampered, map[string]string{"Idempotency-Key": "tampered"})
	if rec.Code != 409 {
		t.Fatalf("tampering accepted: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": plan.SHA256})
	if rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var first api.RoutePolicyApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Receipt.Verification.Status != "satisfied" || first.GatewayState != "unobserved" {
		t.Fatalf("receipt=%+v", first)
	}
	replay := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": plan.SHA256})
	var second api.RoutePolicyApplyResponse
	if err := json.Unmarshal(replay.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if replay.Code != 200 || !second.Replayed || second.Receipt.ID != first.Receipt.ID {
		t.Fatalf("replay=%d %+v", replay.Code, second)
	}
	recovered := e.do(t, "GET", "/v1/apps/"+slug+"/route-policy/receipts/"+first.Receipt.ID, nil, nil)
	if recovered.Code != 200 {
		t.Fatalf("receipt recovery: %d", recovered.Code)
	}
	tampered = apply
	tampered.ThrottleBurst++
	rec = e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", tampered, map[string]string{"Idempotency-Key": plan.SHA256})
	if rec.Code != 409 {
		t.Fatal("key reuse was accepted")
	}
	other := mustSeedEdgeRuleApp(t, e, "route-other")
	if rec := e.do(t, "GET", "/v1/apps/"+other+"/route-policy/receipts/"+first.Receipt.ID, nil, nil); rec.Code != 404 {
		t.Fatal("receipt crossed app boundary")
	}
	if rec := e.do(t, "POST", "/v1/apps/"+other+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "other"}); rec.Code != 409 {
		t.Fatal("plan crossed app boundary")
	}
}

type routePolicyFailApplyNotifier struct{ noopNotifier }

func (routePolicyFailApplyNotifier) Notify(_ context.Context, channel, body string) error {
	if channel != db.NotifyEdgeRuleChanged {
		return nil
	}
	var changed db.EdgeRuleChangedPayload
	if err := json.Unmarshal([]byte(body), &changed); err != nil {
		return err
	}
	if changed.Phase == "apply" {
		return errors.New("forced post-commit notification failure")
	}
	return nil
}

func TestRoutePolicyServerConvergenceFailureKeepsReceipt(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "route-convergence")
	e.s.notif = routePolicyFailApplyNotifier{}
	request := routePolicyServerRequest(t)
	plan := routePolicyServerPlan(t, e, slug, request)
	apply := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, ExpectedPlanSHA256: plan.SHA256, Confirm: true}
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "converging"})
	var response api.RoutePolicyApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || response.GatewayState != "converging" || response.Receipt.Verification.Status != "satisfied" {
		t.Fatalf("committed result lost: %d %+v", rec.Code, response)
	}
	// Preparation can become unavailable on retry; the durable outcome remains recoverable.
	e.s.edgeRuleFleetRequired = true
	rec = e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "converging"})
	var replay api.RoutePolicyApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !replay.Replayed || replay.GatewayState != "unknown" || replay.Receipt.ID != response.Receipt.ID {
		t.Fatalf("retry=%d %+v", rec.Code, replay)
	}
}

func TestRoutePolicyServerScopesMFAAndStrictValidation(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "route-scope")
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	request := routePolicyServerRequest(t)
	plan := routePolicyServerPlan(t, e, slug, request)
	apply := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, ExpectedPlanSHA256: plan.SHA256, Confirm: true}
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, map[string]string{"Idempotency-Key": "read-only"})
	if rec.Code != 403 {
		t.Fatalf("read key could apply: %d", rec.Code)
	}
	e = setup(t, api.PlanPro)
	slug = mustSeedEdgeRuleApp(t, e, "strict-policy")
	for _, body := range []any{map[string]any{"requirements": request.Requirements, "unexpected": true}, api.RoutePolicyPlanRequest{}, map[string]any{"requirements": request.Requirements, "throttle_burst": -1}} {
		if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/plan", body, nil); rec.Code != 400 {
			t.Fatalf("invalid plan accepted: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/route-policy/apply", apply, nil); rec.Code != 400 {
		t.Fatal("missing idempotency key accepted")
	}
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	req := httptest.NewRequest("POST", "/v1/apps/hidden/route-policy/plan", nil)
	req.AddCookie(pending)
	recorder := httptest.NewRecorder()
	mfa.h.ServeHTTP(recorder, req)
	if recorder.Code != 403 {
		t.Fatalf("pending MFA bypass: %d", recorder.Code)
	}
	foreign, err := e.store.CreateAccount(t.Context(), "foreign-route@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "foreign-route", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "POST", "/v1/apps/foreign-route/route-policy/plan", request, nil); rec.Code != 404 {
		t.Fatalf("foreign plan exposed: %d", rec.Code)
	}
}
