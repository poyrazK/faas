package state_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — a stage's runs of a workflow never count against
// production's failure policy of the same name (amendment 2026-11-28).
func TestStoredWorkflowFailurePolicyIgnoresStageRuns(t *testing.T) {
	storedWorkflowFailureStores(t, func(t *testing.T, store state.Store) {
		app, production := storedWorkflowFailureFixture(t, store)
		policies := store.(state.StoredWorkflowFailurePolicyStore)
		ctx := t.Context()
		enabled := true
		req := api.SetStoredWorkflowFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 3, MinCompletedRuns: 5, WindowSeconds: 300}
		if _, err := policies.SetStoredWorkflowFailurePolicy(ctx, app.ID, "nightly", req); err != nil {
			t.Fatal(err)
		}
		stage, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:stage", Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, stage.ID); err != nil {
			t.Fatal(err)
		}
		run := func(deploymentID string) {
			t.Helper()
			r := &state.WorkflowRun{AppID: app.ID, WorkflowName: "nightly", DeploymentID: deploymentID,
				DefinitionSnapshot: json.RawMessage(`{"name":"nightly","steps":[{"name":"main","path":"/report"}]}`)}
			if err := store.CreateWorkflowRun(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkWorkflowRunStatus(ctx, r.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 5; i++ {
			run(stage.ID)
		}
		run(production.ID)
		out, err := policies.GetStoredWorkflowFailurePolicy(ctx, app.ID, "nightly")
		if err != nil || out.ObservedFailures != 1 || out.ObservedCompletedRuns != 1 {
			t.Fatalf("production policy = %+v, %v; want only production's run counted", out, err)
		}
	})
}
