package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleaseReviewHTTPAdmission(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "mcp-reviewed", Type: state.AppTypeApp, RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	response := e.do(t, "GET", "/v1/apps/mcp-reviewed/release-review", nil, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("review=%d", response.Code)
	}
	var review api.AppReleaseReview
	if err := json.Unmarshal(response.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	zero := 0
	request := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("a", 64), Scope: api.DefaultEnvScope, TrafficPercent: &zero, ExpectedReview: &review}
	ram := 512
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
	rejected := e.do(t, "POST", "/v1/apps/mcp-reviewed/deployments", request, nil)
	if rejected.Code != 409 || !strings.Contains(rejected.Body.String(), "release_review_changed") {
		t.Fatalf("stale admission=%d %s", rejected.Code, rejected.Body)
	}
	if rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 100, 0); err != nil || len(rows) != 0 {
		t.Fatal("stale request created a candidate")
	}
	fresh := e.do(t, "GET", "/v1/apps/mcp-reviewed/release-review", nil, nil)
	if err := json.Unmarshal(fresh.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	staged := e.do(t, "POST", "/v1/apps/mcp-reviewed/deployments", request, nil)
	if staged.Code != http.StatusAccepted {
		t.Fatalf("staging=%d %s", staged.Code, staged.Body)
	}
	var deployment api.DeploymentResponse
	if err := json.Unmarshal(staged.Body.Bytes(), &deployment); err != nil || deployment.TrafficPercent != 0 {
		t.Fatal("reviewed insert changed traffic")
	}
	hundred := 100
	request.TrafficPercent = &hundred
	invalid := e.do(t, "POST", "/v1/apps/mcp-reviewed/deployments", request, nil)
	if invalid.Code != 400 {
		t.Fatal("reviewed admission allowed immediate serving")
	}
}

func TestReleaseReviewHTTPNamedEnvironment(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "mcp-named"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "named-image", Type: state.AppTypeApp, RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.MinInstances = 1
	settings.RAMMB = 512
	spec, err := e.store.PutProjectEnvironmentWorkloadSpec(ctx, e.acct.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	response := e.do(t, "GET", "/v1/apps/named-image/release-review?environment=staging", nil, nil)
	var review api.AppReleaseReview
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &review) != nil || review.EnvironmentID != env.ID || review.WorkloadRevision != spec.Revision {
		t.Fatalf("named review=%d %s", response.Code, response.Body.String())
	}
	zero := 0
	request := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("b", 64), Environment: "staging", TrafficPercent: &zero, ExpectedReview: &review}
	staged := e.do(t, "POST", "/v1/apps/named-image/deployments", request, nil)
	var deployment api.DeploymentResponse
	if staged.Code != 202 || json.Unmarshal(staged.Body.Bytes(), &deployment) != nil || deployment.Scope != "staging" || deployment.TrafficPercent != 0 || deployment.StagedReview == nil || deployment.StagedReview.WorkloadRevision != spec.Revision {
		t.Fatalf("named stage=%d %s", staged.Code, staged.Body.String())
	}
	stored, err := e.store.DeploymentByID(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := e.store.ProjectEnvironmentWorkloadSpecForDeployment(ctx, e.acct.ID, project.ID, stored.ID)
	if err != nil || pinned.Settings.MinInstances != 1 || pinned.Settings.RAMMB != 512 {
		t.Fatal("deployment did not pin target resource settings", err)
	}
	health := e.do(t, "GET", "/v1/apps/named-image/health?environment=staging", nil, nil)
	var assessment api.AppHealthResponse
	if health.Code != 200 || json.Unmarshal(health.Body.Bytes(), &assessment) != nil || assessment.EnvironmentID != env.ID || assessment.Scope != "staging" || assessment.LatestDeploymentID != deployment.ID || len(assessment.ServingDeploymentIDs) != 0 {
		t.Fatalf("scoped health=%d %s", health.Code, health.Body.String())
	}
	if err := e.store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	noServing := ""
	traffic := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &noServing, ExpectedReview: deployment.StagedReview}
	promoted := e.do(t, "PATCH", "/v1/deployments/"+deployment.ID+"/traffic", traffic, nil)
	if promoted.Code != 200 {
		t.Fatalf("reviewed named traffic=%d %s", promoted.Code, promoted.Body.String())
	}
	settings.RAMMB = 384
	if _, err := e.store.PutProjectEnvironmentWorkloadSpec(ctx, e.acct.ID, project.ID, "staging", app.ID, spec.Revision, settings); err != nil {
		t.Fatal(err)
	}
	rejected := e.do(t, "POST", "/v1/apps/named-image/deployments", request, nil)
	if rejected.Code != 409 {
		t.Fatalf("stale named stage=%d %s", rejected.Code, rejected.Body.String())
	}
	staleTraffic := e.do(t, "PATCH", "/v1/deployments/"+deployment.ID+"/traffic", traffic, nil)
	if staleTraffic.Code != 409 || !strings.Contains(staleTraffic.Body.String(), api.CodeReleaseReviewChanged) {
		t.Fatalf("stale traffic review=%d %s", staleTraffic.Code, staleTraffic.Body.String())
	}
	if wrong := e.do(t, "GET", "/v1/apps/named-image/health?environment=missing", nil, nil); wrong.Code != 404 {
		t.Fatal("unknown environment silently used default health")
	}
}
