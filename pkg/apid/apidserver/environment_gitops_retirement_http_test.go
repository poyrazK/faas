package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// This exercises the real handlers and production backend without claiming
// native qualification. A never-executed workload can retire without VM work.
func TestEnvironmentGitOpsRetirementHTTPReviewAndBackendCutover(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "enforce", ApprovalPolicy: "manual", Prune: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Variables: map[string]string{"MODE": "retained"}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Desired: initial, ApprovedBy: "test-owner"})
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), account.ID, source.ID)
	if err != nil || !adoption.CanApply() {
		t.Fatalf("adoption: %+v %v", adoption, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), account.ID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "initial", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := retirementHTTPPlan(t, store, lease, initial)
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("b", 40)
	srv.githubd = &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}, resolvedSHA: sha, archive: environmentGitOpsArchiveDefinition(t, "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\nworkload_pruning_policy: retain\nworkloads: {}\n")}
	// Only this isolated test executor opens the enforce-mode API gate.
	srv.seedEnvPlatformFlags(acceptanceEnforcementEnv)
	srv.environmentGitOpsEnforcementWorkerLive.Store(true)
	preview := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "preview", map[string]string{"commit_sha": sha}, srv.previewEnvironmentGitPlan)
	var reviewed api.PreviewEnvironmentGitPlanResponse
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &reviewed) != nil {
		t.Fatalf("review: %d %s", preview.Code, preview.Body.String())
	}
	if len(reviewed.Plan.RetirementImpacts) != 1 || reviewed.Plan.RetirementImpacts[0].AppID != app.ID {
		t.Fatalf("missing retirement review: %s", preview.Body.String())
	}
	approved := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "approve", map[string]any{"commit_sha": sha, "definition_digest": reviewed.Plan.DesiredDigest, "expected_generation": source.Generation}, srv.approveEnvironmentGitRevision)
	if approved.Code != http.StatusAccepted {
		t.Fatalf("approval: %d %s", approved.Code, approved.Body.String())
	}
	lease, err = store.ClaimEnvironmentGitOps(t.Context(), "retirement", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	plan = retirementHTTPPlan(t, store, lease, desired)
	backend := &environmentGitOpsBackend{server: srv, intent: store, effects: store}
	if steps, err := backend.Apply(t.Context(), lease, plan); err != nil || len(steps) != 1 {
		t.Fatalf("reservation: %+v %v", steps, err)
	}
	plan = retirementHTTPPlan(t, store, lease, desired)
	if ready, err := backend.VerifyRuntime(t.Context(), lease, plan); err != nil || !ready {
		t.Fatalf("never-executed retirement: %t %v", ready, err)
	}
	response := gitOpsHandlerRequest(t, srv, account, http.MethodGet, "status", nil, srv.getEnvironmentGitOps)
	var status api.EnvironmentGitOpsStatusResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || len(status.WorkloadRetirements) != 1 || status.WorkloadRetirements[0].Phase != "retired" || status.WorkloadRetirements[0].AppID != app.ID {
		t.Fatalf("durable retirement status: %d %s", response.Code, response.Body.String())
	}
	values, err := store.ListAppEnvInScope(t.Context(), account.ID, app.ID, "production")
	if err != nil || len(values) != 1 || values[0].Value != "retained" {
		t.Fatalf("retained configuration: %+v %v", values, err)
	}
	other, err := store.CreateAccount(t.Context(), "retirement-neighbor@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	response = gitOpsHandlerRequest(t, srv, other, http.MethodGet, "status", nil, srv.getEnvironmentGitOps)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-account retirement leaked: %d", response.Code)
	}
}

func retirementHTTPPlan(t *testing.T, store *state.MemStore, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) environmentsync.Plan {
	t.Helper()
	observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation, Prune: lease.Source.Spec.Prune, Now: time.Now(), Overrides: observed.Overrides})
	if err != nil || !plan.CanApply() {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	return plan
}
