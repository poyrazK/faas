package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteHealthAPIManualWorkerAndRecovery(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "route-health-api")
	app, _ := (*e.Store()).AppBySlug(t.Context(), slug)
	stable, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	d, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + slug + "/route-health"
	zero := int64(0)
	if rec := e.Do(t, "GET", base+"/gate", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"revision":0`) {
		t.Fatalf("default: %d %s", rec.Code, rec.Body)
	}
	req := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout"}}}
	if rec := e.Do(t, "PUT", base+"/gate", req, nil); rec.Code != 200 {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "PUT", base+"/gate", req, nil); rec.Code != 409 {
		t.Fatal("stale revision accepted")
	}
	if rec := e.Do(t, "GET", base+"/deployments/"+d.ID, nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "telemetry_unavailable") {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	path := "/v1/deployments/" + d.ID + "/canary/advance"
	if rec := e.Do(t, "POST", path, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteHealthBlocked) || !strings.Contains(rec.Body.String(), "routes health report") {
		t.Fatalf("manual: %d %s", rec.Code, rec.Body)
	}
	if err := (*e.Store()).SetDeploymentCanaryState(t.Context(), d.ID, "balanced", 0, 4, time.Now().Add(-time.Hour), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}
	worker := httptest.NewRequest("POST", path, strings.NewReader(`{"expected_step":0}`))
	worker.SetPathValue("id", d.ID)
	rec := httptest.NewRecorder()
	(*e.S()).AdvanceCanaryByWorker(rec, worker, (*e.Acct()))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteHealthBlocked) {
		t.Fatalf("worker: %d %s", rec.Code, rec.Body)
	}
	current, _ := (*e.Store()).DeploymentByID(t.Context(), d.ID)
	if current.CanaryStep != 0 || current.TrafficPercent != 1 {
		t.Fatal("blocked request changed traffic")
	}
	for _, action := range []string{"advance", "promote"} {
		if rec := e.Do(t, "POST", "/v1/apps/"+slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: action}, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), api.CodeRouteHealthBlocked) {
			t.Fatalf("legacy %s: %d %s", action, rec.Code, rec.Body)
		}
	}
	if rec := e.Do(t, "POST", "/v1/apps/"+slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: "abort"}, nil); rec.Code != 200 {
		t.Fatalf("abort %d %s", rec.Code, rec.Body)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "health-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	(*e.Key()) = key
	if rec := e.Do(t, "GET", base+"/gate", nil, nil); rec.Code != 200 {
		t.Fatal("read scope rejected")
	}
	if rec := e.Do(t, "PUT", base+"/gate", req, nil); rec.Code != 403 {
		t.Fatal("read scope wrote gate")
	}
	if rec := e.Do(t, "GET", base+"/deployments/"+uuid.NewString(), nil, nil); rec.Code != 404 {
		t.Fatal("missing deployment leaked")
	}
}
func TestRouteHealthAPIMFAValidationAndPlan(t *testing.T) {
	mfa := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	mfa.GenerateEnrolledAccount(t)
	pending := mfa.MfaIssueWithPending(t, true)
	for _, entry := range []struct{ method, path string }{{"GET", "/gate"}, {"PUT", "/gate"}, {"GET", "/deployments/" + uuid.NewString()}} {
		req := httptest.NewRequest(entry.method, "/v1/apps/hidden/route-health"+entry.path, nil)
		req.AddCookie(pending)
		rec := httptest.NewRecorder()
		(*mfa.H()).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("MFA bypass: %d", rec.Code)
		}
	}
	e := apidserver.Setup(t, api.PlanFree)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "health-plan")
	for _, req := range []map[string]any{{"mode": "enforce", "expected_revision": 0, "routes": []any{}}, {"mode": "report", "routes": []any{}}, {"mode": "report", "expected_revision": 0, "routes": []map[string]string{{"method": "GET", "path": "/secret?token=1"}}}} {
		if rec := e.Do(t, "PUT", "/v1/apps/"+slug+"/route-health/gate", req, nil); rec.Code != 400 {
			t.Fatalf("validation %d %s", rec.Code, rec.Body)
		}
	}
	req := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/health"}}}
	if rec := e.Do(t, "PUT", "/v1/apps/"+slug+"/route-health/gate", req, nil); rec.Code != 403 {
		t.Fatalf("plan %d %s", rec.Code, rec.Body)
	}
}
