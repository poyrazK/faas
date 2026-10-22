package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReviewedWorkloadRolloutHTTP(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			ctx := t.Context()
			manifest := state.AppManifest{ExecutionMode: mode, StopSignal: "SIGTERM", StopGracePeriodS: 20}
			if mode == api.ExecutionModeWorker {
				manifest.WorkerReplicas = &state.WorkerScaling{Min: 1, Max: 3}
			}
			app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "native-rollout", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 3, Status: state.AppActive, Manifest: manifest})
			if err != nil {
				t.Fatal(err)
			}
			appRead := e.do(t, http.MethodGet, "/v1/apps/native-rollout", nil, nil)
			var appDTO api.AppResponse
			if appRead.Code != 200 || json.Unmarshal(appRead.Body.Bytes(), &appDTO) != nil || appDTO.Manifest.StopSignal != "SIGTERM" || appDTO.Manifest.StopGracePeriod.Seconds() != 20 || (mode == api.ExecutionModeWorker && appDTO.Manifest.WorkerReplicas == nil) {
				t.Fatal("API omitted reviewed stop/worker policy")
			}
			base, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "registry.example/app@sha256:" + strings.Repeat("a", 64)})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.store.MarkDeploymentLive(ctx, base.ID); err != nil {
				t.Fatal(err)
			}
			var review api.AppReleaseReview
			reviewed := e.do(t, http.MethodGet, "/v1/apps/native-rollout/release-review", nil, nil)
			if reviewed.Code != 200 || json.Unmarshal(reviewed.Body.Bytes(), &review) != nil {
				t.Fatal("missing release review")
			}
			req := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("b", 64), Scope: api.DefaultEnvScope, NativeRollout: mode, ExpectedReview: &review}
			mismatch := req
			mismatch.NativeRollout = api.ExecutionModeRequest
			if got := e.do(t, http.MethodPost, "/v1/apps/native-rollout/deployments", mismatch, nil); got.Code != 400 {
				t.Fatal("allowed request-mode native rollout")
			}
			badTraffic := req
			zero := 0
			badTraffic.TrafficPercent = &zero
			if got := e.do(t, http.MethodPost, "/v1/apps/native-rollout/deployments", badTraffic, nil); got.Code != 400 {
				t.Fatal("allowed zero-traffic worker staging")
			}
			accepted := e.do(t, http.MethodPost, "/v1/apps/native-rollout/deployments", req, map[string]string{"Idempotency-Key": "reviewed-native-one"})
			var d api.DeploymentResponse
			if accepted.Code != 202 || json.Unmarshal(accepted.Body.Bytes(), &d) != nil {
				t.Fatalf("native admission=%d %s", accepted.Code, accepted.Body)
			}
			if mode == api.ExecutionModeService && (d.TrafficPercent != 0 || d.RolloutState != "rolling_out") {
				t.Fatal("service bypassed readiness handoff")
			}
			if mode == api.ExecutionModeWorker && d.TrafficPercent != 100 {
				t.Fatal("worker did not use native activation")
			}
			replay := e.do(t, http.MethodPost, "/v1/apps/native-rollout/deployments", req, map[string]string{"Idempotency-Key": "reviewed-native-one"})
			if replay.Code != 202 || replay.Body.String() != accepted.Body.String() {
				t.Fatal("lost-reply retry created another generation")
			}
			stale := e.do(t, http.MethodPost, "/v1/apps/native-rollout/deployments", req, nil)
			if stale.Code != 409 {
				t.Fatalf("stale review=%d %s", stale.Code, stale.Body)
			}
			if err := e.store.MarkDeploymentLive(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			if mode == api.ExecutionModeService {
				old, err := e.store.DeploymentByID(ctx, base.ID)
				if err != nil || old.Status != state.DeployLive || old.TrafficPercent != 100 {
					t.Fatal("service displaced predecessor before readiness")
				}
				fresh := e.do(t, http.MethodGet, "/v1/apps/native-rollout/release-review", nil, nil)
				if json.Unmarshal(fresh.Body.Bytes(), &review) != nil {
					t.Fatal("fresh review")
				}
				recovery := api.RecoverRolloutRequest{Action: "abort", DeploymentID: d.ID, ExpectedPredecessorDeploymentID: base.ID, ExpectedReview: &review, Reason: "recover reviewed service"}
				reqReason := e.do(t, http.MethodPost, "/v1/apps/native-rollout/rollouts/recover", recovery, nil)
				if reqReason.Code != 202 || !strings.Contains(reqReason.Body.String(), "accepted") {
					t.Fatalf("native reverse handoff=%d %s", reqReason.Code, reqReason.Body)
				}
				ram := 512
				if _, err := e.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
					t.Fatal(err)
				}
				changed := e.do(t, http.MethodPost, "/v1/apps/native-rollout/rollouts/recover", recovery, nil)
				if changed.Code != 409 || !strings.Contains(changed.Body.String(), api.CodeReleaseReviewChanged) {
					t.Fatalf("stale recovery=%d %s", changed.Code, changed.Body)
				}
			}
		})
	}
}

func TestReviewedWorkloadRolloutProtectedHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "protected-rollout"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "protected-worker", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 3, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "restricted", Protected: true})
	if err != nil {
		t.Fatal(err)
	}
	response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/release-review?environment=restricted", nil, nil)
	var review api.AppReleaseReview
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &review) != nil {
		t.Fatal("missing protected review")
	}
	// Even a client which clears the observed flag cannot bypass the locked check.
	review.Protected = false
	request := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("a", 64), Environment: "restricted", NativeRollout: api.ExecutionModeWorker, ExpectedReview: &review}
	rejected := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/deployments", request, nil)
	if rejected.Code != 409 {
		t.Fatalf("protected native activation=%d %s", rejected.Code, rejected.Body)
	}
	if rows, err := e.store.ListDeploymentsForApp(ctx, app.ID, 100, 0); err != nil || len(rows) != 0 {
		t.Fatal("protected target admitted deployment")
	}
}

func TestReviewedWorkloadRolloutNamedSettingsHTTP(t *testing.T) {
	for _, mode := range []string{api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			ctx := t.Context()
			project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "named-native"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "named-native", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 3, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"})
			if err != nil {
				t.Fatal(err)
			}
			settings, err := state.WorkloadSettingsFromApp(app)
			if err != nil {
				t.Fatal(err)
			}
			settings.Manifest.ExecutionMode = mode
			settings.RAMMB = 256
			if _, err := e.store.PutProjectEnvironmentWorkloadSpec(ctx, e.acct.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
				t.Fatal(err)
			}
			response := e.do(t, http.MethodGet, "/v1/apps/named-native/release-review?environment=staging", nil, nil)
			var review api.AppReleaseReview
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &review) != nil {
				t.Fatal("missing named review")
			}
			req := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("a", 64), Environment: "staging", ExpectedReview: &review, NativeRollout: mode}
			accepted := e.do(t, http.MethodPost, "/v1/apps/named-native/deployments", req, nil)
			var d api.DeploymentResponse
			if accepted.Code != 202 || json.Unmarshal(accepted.Body.Bytes(), &d) != nil || d.Scope != "staging" {
				t.Fatalf("named activation=%d %s", accepted.Code, accepted.Body)
			}
			stored, err := e.store.ProjectEnvironmentWorkloadSpecForDeployment(ctx, e.acct.ID, project.ID, d.ID)
			if err != nil || stored.Settings.Manifest.ExecutionMode != mode || stored.Settings.RAMMB != 256 {
				t.Fatal("native rollout ignored selected environment settings", err)
			}
			original, err := e.store.AppByID(ctx, app.ID)
			if err != nil || original.Manifest.ExecutionMode != "" || original.RAMMB != 128 {
				t.Fatal("named rollout changed default intent")
			}
		})
	}
}
