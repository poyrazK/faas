package imaged

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635: only a deployment an environment branch created from
// its parent's build may go live without a snapshot.
func TestEnvironmentBranchActivationIsOnlyForBranchDeployments(t *testing.T) {
	for _, mode := range []string{"branch", "ordinary", "instance_supplied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@branch.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "branched"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, RAMMB: 128})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:branch"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, parent.ID, "/branch.ext4", "layers/branch.ext4", 4096); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := store.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
				AccountID: account.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-1",
				ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: 2,
			}, api.MustLimitsFor(account.Plan)); err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeploymentForEnvironmentBranch(ctx, account.ID, project.ID, "pr-1", app.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ordinary" {
				dep, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "pr-1", Kind: state.DeploymentKindImage, ImageDigest: "sha256:other", Status: state.DeploySnapshotting})
				if err != nil {
					t.Fatal(err)
				}
			}
			ready := deploymentReadyPayload{DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeRequest, NoSnapshotReason: state.EnvironmentBranchActivationReason}
			if mode == "instance_supplied" {
				ready.InstanceID = uuid.NewString()
			}
			h := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger())
			err = h.handleDeploymentReady(ctx, ready)
			if (err == nil) != (mode == "branch") {
				t.Fatalf("activation: %v", err)
			}
			actual, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (actual.Status == state.DeployLive) != (mode == "branch") {
				t.Fatalf("deployment status = %s", actual.Status)
			}
		})
	}
}
