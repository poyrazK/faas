package apidserver_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func publishRollbackAPIWorker(t *testing.T, s *state.MemStore, app state.App, deploymentID string) state.Instance {
	t.Helper()
	ctx := t.Context()
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInstanceWithMode(ctx, app.ID, deploymentID, "cold_booting", app.RAMMB, node.ID, uuid.NewString(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	i, err = s.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: i.ID, NodeID: i.NodeID, WakeID: i.WakeID, ExpectedState: "cold_booting", Netns: "rollback-worker-test", HostIP: "10.0.0.1", GuestUID: 20001, Fence: fence.SecretFence, ConfigFence: fence})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestCheckedWorkerRollbackBlocksAndResumesAfterLostReadiness(t *testing.T) {
	for _, change := range []string{"stopped", "config-changed"} {
		t.Run(change, func(t *testing.T) {
			e, app, target, current := apidserver.CheckedRollbackAPIFixture(t)
			s, ctx := *e.Store(), t.Context()
			manifest := app.Manifest
			manifest.ExecutionMode = api.ExecutionModeWorker
			var err error
			app, err = s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
			if err != nil {
				t.Fatal(err)
			}
			op, err := s.CreateCheckedRollback(ctx, app.AccountID, app.ID, target.ID, current.ID, "recover worker")
			if err != nil {
				t.Fatal(err)
			}
			worker := publishRollbackAPIWorker(t, s, app, target.ID)
			if err = s.MarkDeploymentLive(ctx, target.ID); err != nil {
				t.Fatal(err)
			}
			if change == "stopped" {
				err = s.UpdateInstanceState(ctx, worker.ID, "stopped")
			} else {
				err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = (*e.S()).CheckedRollbackSweep(ctx); err != nil {
				t.Fatal(err)
			}
			blocked, err := s.GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
			if err != nil || blocked.Status != "blocked" || blocked.Code != "worker_rollback_not_ready" || len(blocked.Blockers) != 1 {
				t.Fatalf("missing readiness blocker: %+v %v", blocked, err)
			}
			serving, err := s.DeploymentByID(ctx, current.ID)
			if err != nil || serving.Status != state.DeployLive || serving.TrafficPercent != 100 {
				t.Fatalf("lost serving release: %+v %v", serving, err)
			}
			if change == "config-changed" {
				if err = s.UpdateInstanceState(ctx, worker.ID, "stopped"); err != nil {
					t.Fatal(err)
				}
			}
			publishRollbackAPIWorker(t, s, app, target.ID)
			if err = (*e.S()).CheckedRollbackSweep(ctx); err != nil {
				t.Fatal(err)
			}
			completed, err := s.GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
			if err != nil || completed.Status != "complete" {
				t.Fatalf("worker readiness recovery did not resume rollback: %+v %v", completed, err)
			}
		})
	}
}
