package deployreadiness

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019120041
func TestReadinessVerifiedReleaseUsesAdmissionPolicy(t *testing.T) {
	store := state.NewMemStore()
	account, project, app := seedReadiness(t, store)
	app.Manifest.RevisionPinTTLSeconds = 3600
	_, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest})
	must(t, err)
	baseline := seedReadinessGraph(t, store, account, project, app, "production")
	request := readinessRequest()
	request.Atomic = true
	request.Verify = true
	config := Config{CriticalProbesConfigured: true}
	missing := Check(t.Context(), store, account, project.Slug, request, config)
	if countCode(missing, "verification_policy_unavailable") != 1 {
		t.Fatal("missing probes passed", missing)
	}
	zero := int64(0)
	_, err = store.SetRouteHealthGate(t.Context(), account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/health", Probe: &api.RouteHealthProbe{Path: "/health"}}}})
	must(t, err)
	ready := Check(t.Context(), store, account, project.Slug, request, config)
	if ready.Status != "ready" {
		t.Fatalf("verified release prerequisites did not pass: %+v", ready)
	}
	if _, problem := VerificationPolicy(t.Context(), store, account.ID, baseline); problem != nil {
		t.Fatal("readiness and admission policies disagreed", problem)
	}
	request.ReleaseConfig = []byte(`{"env":{"api":{}}}`)
	request.Workloads[0].Workload.EnvKeys = []string{"REGION"}
	must(t, store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "REGION", "retained-only"))
	cleared := Check(t.Context(), store, account, project.Slug, request, config)
	if countCode(cleared, "runtime_key_missing") != 1 {
		t.Fatal("empty replacement inherited previous variables", cleared)
	}
}

// adr: 20261019120041
func TestReadinessPromotionAggregatesPreviewAndApproval(t *testing.T) {
	store := state.NewMemStore()
	account, project, app := seedReadiness(t, store)
	_, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	must(t, err)
	app.Manifest.RevisionPinTTLSeconds = 3600
	_, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest})
	must(t, err)
	seedReadinessGraph(t, store, account, project, app, "production")
	seedReadinessGraph(t, store, account, project, app, "staging")
	request := api.ProjectDeploymentCheckRequest{Environment: "production", FromEnvironment: "staging", SyncConfig: true}
	config := Config{PromotionPreview: func(_ context.Context, _ state.Account, _ string, req api.ProjectDeploymentCheckRequest) (api.ProjectEnvironmentPromotionPreviewResponse, *api.Problem) {
		if req.FromEnvironment != "staging" {
			t.Fatal("source environment was lost")
		}
		return api.ProjectEnvironmentPromotionPreviewResponse{PromotionHash: "current-identity", CanPromote: true}, nil
	}}
	ready := Check(t.Context(), store, account, project.Slug, request, config)
	if ready.Status != "ready" || len(ready.StateHash) != 64 {
		t.Fatal("valid promotion preview did not pass", ready)
	}
	config.PromotionPreview = func(context.Context, state.Account, string, api.ProjectDeploymentCheckRequest) (api.ProjectEnvironmentPromotionPreviewResponse, *api.Problem) {
		return api.ProjectEnvironmentPromotionPreviewResponse{PromotionHash: "changed-identity", CanPromote: false, ApprovalRequired: true, BlockingReasons: []string{"source qualification expired", "verified promotion unavailable"}}, nil
	}
	request.ExpectedStateHash = ready.StateHash
	blocked := Check(t.Context(), store, account, project.Slug, request, config)
	for _, code := range []string{"promotion_blocked", "promotion_approval_required", "deployment_inputs_changed"} {
		if countCode(blocked, code) == 0 {
			t.Fatal("promotion blocker omitted", code, blocked)
		}
	}
}

func seedReadinessGraph(t *testing.T, store *state.MemStore, account state.Account, project state.Project, app state.App, scope string) state.ProjectReleaseSet {
	t.Helper()
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball})
	must(t, err)
	must(t, store.SetDeploymentRootfs(t.Context(), deployment.ID, "/rootfs/check", "layers/check-"+scope, 4096))
	must(t, store.MarkDeploymentLive(t.Context(), deployment.ID))
	graph, err := store.PublishProjectReleaseSet(t.Context(), account.ID, project.ID, scope, 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: deployment.ID}})
	must(t, err)
	return graph
}
