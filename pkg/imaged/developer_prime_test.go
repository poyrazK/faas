package imaged

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261122200000 — a developer environment's own deployment goes live
// from its RUNNING prime; nothing else may use the developer_prime proof.
func TestDeveloperPrimeActivationIsOnlyForDeveloperPrimes(t *testing.T) {
	for _, mode := range []string{"developer", "production_app", "branch_seed", "stopped_prime", "no_instance"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, uuid.NewString()+"@devprime.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			previewOf := "web"
			if mode == "production_app" {
				previewOf = ""
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "dev-web-" + uuid.NewString()[:8], PreviewOfSlug: previewOf, Status: state.AppActive, RAMMB: 128, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			reason := ""
			if mode == "branch_seed" {
				reason = state.EnvironmentBranchSeedReason
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:dev", Status: state.DeploySnapshotting, Reason: reason})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, "/dev.ext4", "layers/dev.ext4", 4096); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AppendDeploymentStage(ctx, dep.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
				t.Fatal(err)
			}
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			ins, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, "cold_booting", app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
			if err != nil {
				t.Fatal(err)
			}
			values, err := store.RuntimeAppValuesForDeployment(ctx, account.ID, app.ID, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			fence, err := state.NewRuntimeAppConfigFence(values)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: account.ID, AppID: app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
				ExpectedState: "cold_booting", Netns: "dev-prime", HostIP: "10.0.0.1", GuestUID: 20001, Fence: fence.SecretFence, ConfigFence: fence}); err != nil {
				t.Fatal(err)
			}
			if mode == "stopped_prime" {
				if err := store.UpdateInstanceState(ctx, ins.ID, "stopped"); err != nil {
					t.Fatal(err)
				}
			}
			ready := deploymentReadyPayload{DeploymentID: dep.ID, ExecutionMode: api.ExecutionModeRequest, InstanceID: ins.ID, NoSnapshotReason: state.DeveloperPrimeActivationReason}
			if mode == "no_instance" {
				ready.InstanceID = ""
			}
			h := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger())
			err = h.handleDeploymentReady(ctx, ready)
			want := mode == "developer"
			if (err == nil) != want {
				t.Fatalf("activation: %v", err)
			}
			actual, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (actual.Status == state.DeployLive) != want {
				t.Fatalf("deployment status = %s", actual.Status)
			}
		})
	}
}
