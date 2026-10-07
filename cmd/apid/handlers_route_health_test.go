package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteHealthAPIManualWorkerAndRecovery(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "route-health-api")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	stable, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	d, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + slug + "/route-health"
	zero := int64(0)
	if rec := e.do(t, "GET", base+"/gate", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"revision":0`) {
		t.Fatalf("default: %d %s", rec.Code, rec.Body)
	}
	req := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout"}}}
	if rec := e.do(t, "PUT", base+"/gate", req, nil); rec.Code != 200 {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PUT", base+"/gate", req, nil); rec.Code != 409 {
		t.Fatal("stale revision accepted")
	}
	if rec := e.do(t, "GET", base+"/deployments/"+d.ID, nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "telemetry_unavailable") {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	path := "/v1/deployments/" + d.ID + "/canary/advance"
	if rec := e.do(t, "POST", path, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteHealthBlocked) || !strings.Contains(rec.Body.String(), "routes health report") {
		t.Fatalf("manual: %d %s", rec.Code, rec.Body)
	}
	if err := e.store.SetDeploymentCanaryState(t.Context(), d.ID, "balanced", 0, 4, time.Now().Add(-time.Hour), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}
	worker := httptest.NewRequest("POST", path, strings.NewReader(`{"expected_step":0}`))
	worker.SetPathValue("id", d.ID)
	rec := httptest.NewRecorder()
	e.s.advanceCanaryByWorker(rec, worker, e.acct)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteHealthBlocked) {
		t.Fatalf("worker: %d %s", rec.Code, rec.Body)
	}
	current, _ := e.store.DeploymentByID(t.Context(), d.ID)
	if current.CanaryStep != 0 || current.TrafficPercent != 1 {
		t.Fatal("blocked request changed traffic")
	}
	for _, action := range []string{"advance", "promote"} {
		if rec := e.do(t, "POST", "/v1/apps/"+slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: action}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteHealthBlocked) {
			t.Fatalf("legacy %s: %d %s", action, rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: "abort"}, nil); rec.Code != 200 {
		t.Fatalf("abort %d %s", rec.Code, rec.Body)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "health-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "GET", base+"/gate", nil, nil); rec.Code != 200 {
		t.Fatal("read scope rejected")
	}
	if rec := e.do(t, "PUT", base+"/gate", req, nil); rec.Code != 403 {
		t.Fatal("read scope wrote gate")
	}
	if rec := e.do(t, "GET", base+"/deployments/"+uuid.NewString(), nil, nil); rec.Code != 404 {
		t.Fatal("missing deployment leaked")
	}
}
func TestRouteHealthAPIMFAValidationAndPlan(t *testing.T) {
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	for _, entry := range []struct{ method, path string }{{"GET", "/gate"}, {"PUT", "/gate"}, {"GET", "/deployments/" + uuid.NewString()}} {
		req := httptest.NewRequest(entry.method, "/v1/apps/hidden/route-health"+entry.path, nil)
		req.AddCookie(pending)
		rec := httptest.NewRecorder()
		mfa.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("MFA bypass: %d", rec.Code)
		}
	}
	e := setup(t, api.PlanFree)
	slug := mustSeedEdgeRuleApp(t, e, "health-plan")
	for _, req := range []map[string]any{{"mode": "enforce", "expected_revision": 0, "routes": []any{}}, {"mode": "report", "routes": []any{}}, {"mode": "report", "expected_revision": 0, "routes": []map[string]string{{"method": "GET", "path": "/secret?token=1"}}}} {
		if rec := e.do(t, "PUT", "/v1/apps/"+slug+"/route-health/gate", req, nil); rec.Code != 400 {
			t.Fatalf("validation %d %s", rec.Code, rec.Body)
		}
	}
	req := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/health"}}}
	if rec := e.do(t, "PUT", "/v1/apps/"+slug+"/route-health/gate", req, nil); rec.Code != 403 {
		t.Fatalf("plan %d %s", rec.Code, rec.Body)
	}
}
