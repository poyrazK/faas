package main

// ADR-448: saved intent writes require deploy scope; checks bind captured evidence.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSavedRouteRequirementsServer(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "saved-route")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:saved", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte(`{"openapi":"3.1.0","paths":{"/health":{"get":{}},"/new":{"post":{}}}}`)
	if err := e.store.UpsertDeploymentOpenAPIDoc(t.Context(), deployment.ID, app.AccountID, app.ID, doc, "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	config, err := routerequirements.ParsePreview([]byte(`{"version":2,"public":[{"method":"GET","path":"/health","reason":"private-rationale"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + slug + "/route-requirements"
	zero := int64(0)
	request := api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: config}
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 404 {
		t.Fatalf("missing intent: %d %s", rec.Code, rec.Body)
	}
	rec := e.do(t, "PUT", path, request, nil)
	var saved api.SavedRouteRequirements
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &saved) != nil || saved.Revision != 1 || saved.AppID != app.ID || strings.Contains(rec.Body.String(), "private-rationale") {
		t.Fatalf("save/privacy: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 200 || strings.Contains(rec.Body.String(), "private-rationale") {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PUT", path, request, nil); rec.Code != 409 {
		t.Fatalf("stale save: %d %s", rec.Code, rec.Body)
	}
	check := api.CheckRouteRequirementsRequest{DeploymentID: deployment.ID, ExpectedRevision: &saved.Revision}
	rec = e.do(t, "POST", path+"/check", check, nil)
	var result api.RouteRequirementsCheck
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Report.Status != "violated" || result.Report.Coverage.RouteCount != 2 || result.RequirementsSHA256 != saved.SHA256 || result.DeploymentID != deployment.ID {
		t.Fatalf("uncovered endpoint: %d %s", rec.Code, rec.Body)
	}
	stale := int64(2)
	check.ExpectedRevision = &stale
	if rec := e.do(t, "POST", path+"/check", check, nil); rec.Code != 409 {
		t.Fatalf("stale check: %d %s", rec.Code, rec.Body)
	}
	check.ExpectedRevision = nil
	if err := e.store.DeleteDeploymentOpenAPIDoc(t.Context(), deployment.ID, app.AccountID); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, "POST", path+"/check", check, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Report.Status != "unknown" || result.Report.Coverage.Status != "unavailable" {
		t.Fatalf("missing capture: %d %s", rec.Code, rec.Body)
	}
	other := mustSeedEdgeRuleApp(t, e, "other-saved-route")
	if rec := e.do(t, "PUT", "/v1/apps/"+other+"/route-requirements", request, nil); rec.Code != 200 {
		t.Fatal("seed other app")
	}
	if rec := e.do(t, "POST", "/v1/apps/"+other+"/route-requirements/check", check, nil); rec.Code != 404 {
		t.Fatalf("foreign deployment: %d %s", rec.Code, rec.Body)
	}
}

func TestSavedRouteRequirementsServerValidationAndScopes(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "saved-scope")
	path := "/v1/apps/" + slug + "/route-requirements"
	config := api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "private"}}}
	zero := int64(0)
	request := api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: config}
	for _, body := range []any{
		api.SaveRouteRequirementsRequest{Requirements: config},
		map[string]any{"expected_revision": 0, "requirements": config, "unknown": true},
		map[string]any{"expected_revision": 0, "requirements": map[string]any{"version": 2, "public": []any{map[string]any{"method": "GET", "path": "/health", "reason": "", "unexpected": true}}}},
	} {
		if rec := e.do(t, "PUT", path, body, nil); rec.Code != 400 {
			t.Fatalf("invalid save accepted: %d %s", rec.Code, rec.Body)
		}
	}
	request.Requirements.Public[0].Reason = ""
	if rec := e.do(t, "PUT", path, request, nil); rec.Code != 400 {
		t.Fatalf("empty public rationale accepted: %d %s", rec.Code, rec.Body)
	}
	request.Requirements.Public[0].Reason = "private"
	if rec := e.do(t, "PUT", path, request, nil); rec.Code != 200 {
		t.Fatalf("seed: %d %s", rec.Code, rec.Body)
	}
	for _, body := range []any{api.CheckRouteRequirementsRequest{}, map[string]any{"deployment_id": uuid.NewString(), "unexpected": true}, map[string]any{"deployment_id": uuid.NewString(), "expected_revision": 0}} {
		if rec := e.do(t, "POST", path+"/check", body, nil); rec.Code != 400 {
			t.Fatalf("invalid check accepted: %d %s", rec.Code, rec.Body)
		}
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "saved-read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatalf("read scope rejected: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "POST", path+"/check", api.CheckRouteRequirementsRequest{DeploymentID: uuid.NewString()}, nil); rec.Code != 404 {
		t.Fatalf("read scope check rejected: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PUT", path, request, nil); rec.Code != 403 {
		t.Fatalf("read scope wrote intent: %d %s", rec.Code, rec.Body)
	}
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	for _, methodPath := range [][2]string{{"GET", path}, {"PUT", path}, {"POST", path + "/check"}} {
		req := httptest.NewRequest(methodPath[0], methodPath[1], nil)
		req.AddCookie(pending)
		recorder := httptest.NewRecorder()
		mfa.h.ServeHTTP(recorder, req)
		if recorder.Code != 403 {
			t.Fatalf("pending MFA bypass: %s %d", methodPath[0], recorder.Code)
		}
	}
}
