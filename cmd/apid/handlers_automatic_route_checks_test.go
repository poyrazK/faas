package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAutomaticRouteChecksServerCaptureAndFreshness(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "automatic-routes")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:automatic", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	intent := api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "private-explanation"}}}}
	base := "/v1/apps/" + slug + "/route-requirements"
	if rec := e.do(t, "PUT", base, intent, nil); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	path := base + "/checks/" + deployment.ID
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 404 {
		t.Fatal("uncaptured deployment had a result")
	}
	doc := json.RawMessage(`{"openapi":"3.1.0","paths":{"/health":{"get":{}}}}`)
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug+"/deployments/"+deployment.ID+"/openapi", map[string]any{"set_doc": true, "set_source": true, "doc": doc, "source": "manual_upload"}, nil); rec.Code != 200 {
		t.Fatalf("capture: %d %s", rec.Code, rec.Body)
	}
	read := func() api.AutomaticRouteCheck {
		rec := e.do(t, "GET", path, nil, nil)
		var result api.AutomaticRouteCheck
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || strings.Contains(rec.Body.String(), "private-explanation") {
			t.Fatalf("result/privacy: %d %s", rec.Code, rec.Body)
		}
		return result
	}
	if got := read(); got.State != "pending" || got.Check != nil {
		t.Fatalf("capture handoff: %+v", got)
	}
	if count, err := e.s.drainAutomaticRouteChecks(t.Context()); err != nil || count != 1 {
		t.Fatalf("drain: %d %v", count, err)
	}
	first := read()
	if first.State != "complete" || first.Freshness != "current" || first.Check.Report.Status != "satisfied" {
		t.Fatalf("automatic pass: %+v", first)
	}
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug+"/deployments/"+deployment.ID+"/openapi", map[string]any{"set_doc": true, "set_source": true, "doc": doc, "source": "cold_boot"}, nil); rec.Code != 200 {
		t.Fatal("identical capture")
	}
	if count, err := e.s.drainAutomaticRouteChecks(t.Context()); err != nil || count != 0 {
		t.Fatal("duplicate capture was not suppressed")
	}
	mode := api.ConsumerAuthModeRequired
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Freshness != "stale" || len(got.StaleReasons) != 1 || got.StaleReasons[0] != "configuration_changed" {
		t.Fatalf("old pass freshness: %+v", got)
	}
	if rec := e.do(t, "POST", path+"/refresh", nil, nil); rec.Code != 202 {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body)
	}
	if count, err := e.s.drainAutomaticRouteChecks(t.Context()); err != nil || count != 1 || read().Freshness != "current" {
		t.Fatalf("refresh result: %d %v", count, err)
	}
	newDoc := json.RawMessage(`{"openapi":"3.1.0","paths":{"/health":{"get":{}},"/export":{"post":{}}}}`)
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug+"/deployments/"+deployment.ID+"/openapi", map[string]any{"set_doc": true, "set_source": true, "doc": newDoc, "source": "manual_upload"}, nil); rec.Code != 200 {
		t.Fatalf("new capture: %d %s", rec.Code, rec.Body)
	}
	if count, err := e.s.drainAutomaticRouteChecks(t.Context()); err != nil || count != 1 || read().Check.Report.Status != "violated" {
		t.Fatalf("uncovered route missed: %d %v", count, err)
	}
	other := mustSeedEdgeRuleApp(t, e, "foreign-automatic")
	if rec := e.do(t, "GET", "/v1/apps/"+other+"/route-requirements/checks/"+deployment.ID, nil, nil); rec.Code != 404 {
		t.Fatalf("foreign result: %d %s", rec.Code, rec.Body)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "automatic-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatal("read scope could not read results")
	}
	if rec := e.do(t, "POST", path+"/refresh", nil, nil); rec.Code != 202 {
		t.Fatal("read scope could not request bounded check")
	}
	if rec := e.do(t, "POST", path+"/refresh", map[string]bool{"unknown": true}, nil); rec.Code != 400 {
		t.Fatal("refresh accepted unsupported input")
	}
	if err := e.store.UpdateAccountPlan(t.Context(), e.acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 402 || strings.Contains(rec.Body.String(), "/export") {
		t.Fatalf("old capture entitlement leaked: %d %s", rec.Code, rec.Body)
	}
}

func TestAutomaticRouteChecksMFA(t *testing.T) {
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	path := "/v1/apps/hidden/route-requirements/checks/" + uuid.NewString()
	for _, value := range [][2]string{{"GET", path}, {"POST", path + "/refresh"}} {
		req := httptest.NewRequest(value[0], value[1], nil)
		req.AddCookie(pending)
		rec := httptest.NewRecorder()
		mfa.h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("MFA bypass: %d", rec.Code)
		}
	}
}
