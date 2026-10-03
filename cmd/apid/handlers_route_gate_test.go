package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCanaryRouteGateAPIAdvanceWorkerAndAbort(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "route-gated-canary")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	stable, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + slug + "/route-requirements"
	zero := int64(0)
	request := api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero}
	if rec := e.do(t, "GET", base+"/gate", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"mode":"report"`) {
		t.Fatalf("default: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PUT", base+"/gate", request, nil); rec.Code != 409 {
		t.Fatalf("missing intent: %d %s", rec.Code, rec.Body)
	}
	intent := api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "private gate rationale"}}}}
	if rec := e.do(t, "PUT", base, intent, nil); rec.Code != 200 {
		t.Fatalf("intent: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PUT", base+"/gate", request, nil); rec.Code != 200 {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	path := "/v1/deployments/" + candidate.ID + "/canary/advance"
	if rec := e.do(t, "POST", path, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteGateBlocked) || !strings.Contains(rec.Body.String(), "check_missing") {
		t.Fatalf("missing gate: %d %s", rec.Code, rec.Body)
	}
	doc := json.RawMessage(`{"openapi":"3.1.0","paths":{"/health":{"get":{}}}}`)
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug+"/deployments/"+candidate.ID+"/openapi", map[string]any{"set_doc": true, "set_source": true, "doc": doc, "source": "manual_upload"}, nil); rec.Code != 200 {
		t.Fatalf("capture: %d %s", rec.Code, rec.Body)
	}
	if count, err := e.s.drainAutomaticRouteChecks(t.Context()); count != 1 || err != nil {
		t.Fatalf("check: %d %v", count, err)
	}
	rec := e.do(t, "POST", path, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	var advanced api.CanaryAdvanceResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &advanced) != nil || advanced.RouteGate == nil || advanced.RouteGate.Status != "allowed" || advanced.Deployment.TrafficPercent != 10 {
		t.Fatalf("fresh pass: %d %s", rec.Code, rec.Body)
	}
	mode := api.ConsumerAuthModeRequired
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentCanaryState(t.Context(), candidate.ID, "balanced", 1, 4, time.Now().Add(-time.Hour), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}
	workerReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_step":1}`))
	workerReq.SetPathValue("id", candidate.ID)
	workerRec := httptest.NewRecorder()
	e.s.advanceCanaryByWorker(workerRec, workerReq, e.acct)
	if workerRec.Code != 409 || !strings.Contains(workerRec.Body.String(), "check_stale") {
		t.Fatalf("worker reused old pass: %d %s", workerRec.Code, workerRec.Body)
	}
	current, _ := e.store.DeploymentByID(t.Context(), candidate.ID)
	if current.CanaryStep != 1 || current.TrafficPercent != 10 {
		t.Fatal("blocked worker changed traffic")
	}
	for _, action := range []string{"advance", "promote"} {
		if rec := e.do(t, "POST", "/v1/apps/"+slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: action}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), "use_canary_advance") {
			t.Fatalf("legacy bypass %s: %d %s", action, rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: "abort"}, nil); rec.Code != 200 {
		t.Fatalf("abort: %d %s", rec.Code, rec.Body)
	}
	old, _ := e.store.DeploymentByID(t.Context(), stable.ID)
	if old.TrafficPercent != 100 {
		t.Fatal("abort did not restore stable")
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-gate", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "GET", base+"/gate", nil, nil); rec.Code != 200 {
		t.Fatal("read scope")
	}
	if rec := e.do(t, "PUT", base+"/gate", request, nil); rec.Code != 403 {
		t.Fatalf("read scope toggled enforcement: %d", rec.Code)
	}
}

func TestCanaryRouteGateMFAAndValidation(t *testing.T) {
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	for _, method := range []string{"GET", "PUT"} {
		req := httptest.NewRequest(method, "/v1/apps/hidden/route-requirements/gate", nil)
		req.AddCookie(pending)
		rec := httptest.NewRecorder()
		mfa.h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("MFA bypass: %d", rec.Code)
		}
	}
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "gate-validation")
	for _, request := range []map[string]any{{"mode": "enforce"}, {"mode": "invalid", "expected_revision": 0}, {"mode": "enforce", "expected_revision": -1}} {
		if rec := e.do(t, "PUT", "/v1/apps/"+slug+"/route-requirements/gate", request, nil); rec.Code != 400 {
			t.Fatalf("bad request: %d %s", rec.Code, rec.Body)
		}
	}
}
