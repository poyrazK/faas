package state_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingFailureRechecksActivationAttempt(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, change := range []string{"unchanged", "env", "requeued", "requeued-env", "cancelled", "artifact"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					_, app, target, current := checkedRollbackFixture(t, s, false)
					d, err := s.PrepareDeploymentRollback(ctx, app.ID, target.ID)
					if err != nil {
						t.Fatal(err)
					}
					values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					guarded, err := state.WithDeploymentActivationAttempt(ctx, d, values)
					if err != nil {
						t.Fatal(err)
					}
					switch change {
					case "env", "requeued-env":
						err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new")
					case "cancelled":
						_, _, err = s.CancelDeploymentTx(ctx, d.ID, "test", state.CancelReasonUser)
					case "artifact":
						err = s.SetDeploymentRootfs(ctx, d.ID, "/new/rootfs", "new/rootfs", 4096)
					}
					if err != nil {
						t.Fatal(err)
					}
					if change == "requeued" || change == "requeued-env" {
						if err = s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
							t.Fatal(err)
						}
						if _, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID); err != nil {
							t.Fatal(err)
						}
					}
					before, err := s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					changed, err := s.(state.DeploymentHostingFailureStore).FailDeploymentWithHostingReceipt(guarded, d.ID, hostingFailureRaw(t, d, apihostingreceipt.SmokeFailed), "deployment_smoke_failed", "old attempt failed")
					if err != nil {
						t.Fatal(err)
					}
					after, err := s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					if change == "unchanged" {
						if !changed || after.ErrorCode != "deployment_smoke_failed" {
							t.Fatalf("current failure not recorded: changed=%v status=%s code=%s", changed, after.Status, after.ErrorCode)
						}
					} else if changed || before.Status != after.Status || before.ErrorCode != after.ErrorCode || !bytes.Equal(before.APIHostingReceipt, after.APIHostingReceipt) || !bytes.Equal(before.StageState, after.StageState) {
						t.Fatalf("stale failure modified replacement: changed=%v before=%s after=%s", changed, before.Status, after.Status)
					}
					serving, err := s.DeploymentByID(ctx, current.ID)
					if err != nil || serving.Status != state.DeployLive || serving.TrafficPercent != 100 {
						t.Fatalf("lost serving release: %+v %v", serving, err)
					}
				})
			}
		})
	}
}

func TestPrimeRecoveryClaimRechecksAttemptAndWorkerOwnership(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, change := range []string{"stale", "fresh", "requeued", "promoted", "stopped", "wrong-wake", "wrong-node", "wrong-owner", "new-config", "unguarded"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, d := deploymentAttemptFixture(t, s)
					i := publishAttemptWorker(t, s, app, d, true)
					if change != "fresh" {
						if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "changed"); err != nil {
							t.Fatal(err)
						}
					}
					values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					guarded, err := state.WithDeploymentActivationAttempt(ctx, d, values)
					if err != nil {
						t.Fatal(err)
					}
					expected := i
					switch change {
					case "requeued":
						if err = s.MarkDeploymentSuperseded(ctx, d.ID); err == nil {
							_, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
						}
					case "promoted":
						err = s.MarkDeploymentLive(ctx, d.ID)
					case "stopped":
						err = s.UpdateInstanceState(ctx, i.ID, "stopped")
					case "wrong-wake":
						expected.WakeID = uuid.NewString()
					case "wrong-node":
						expected.NodeID = uuid.NewString()
					case "wrong-owner":
						app.NodeID = uuid.NewString()
					case "new-config":
						err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "changed-again")
					case "unguarded":
						guarded = ctx
					}
					if err != nil {
						t.Fatal(err)
					}
					before, err := s.InstanceByID(ctx, i.ID)
					if err != nil {
						t.Fatal(err)
					}
					owner := s.(state.SnapshotPrimeWorkerRecoveryStore)
					claimed, err := owner.ClaimStalePrimeWorker(guarded, app, expected)
					if change == "requeued" || change == "promoted" || change == "new-config" {
						if !errors.Is(err, state.ErrDeploymentActivationStale) {
							t.Fatalf("changed attempt claim=%v %v", claimed, err)
						}
					} else if change == "unguarded" {
						if !errors.Is(err, state.ErrInvalidArgument) {
							t.Fatalf("unguarded claim=%v %v", claimed, err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					after, err := s.InstanceByID(ctx, i.ID)
					if err != nil {
						t.Fatal(err)
					}
					if change == "stale" {
						if !claimed || after.State != "draining" || !state.State(after.State).CountsForRAM() {
							t.Fatalf("stale worker was not durably withdrawn: claimed=%v state=%s", claimed, after.State)
						}
						if again, err := owner.ClaimStalePrimeWorker(guarded, app, expected); err != nil || !again {
							t.Fatalf("draining retry failed: %v %v", again, err)
						}
						// An operator can undo the configuration edit while teardown
						// retries. A durable drain must still finish in that case.
						if err := s.DeleteAppEnv(ctx, app.AccountID, app.ID, "VERSION"); err != nil {
							t.Fatal(err)
						}
						values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
						if err != nil {
							t.Fatal(err)
						}
						resumed, err := state.WithDeploymentActivationAttempt(ctx, d, values)
						if err != nil {
							t.Fatal(err)
						}
						if again, err := owner.ClaimStalePrimeWorker(resumed, app, expected); err != nil || !again {
							t.Fatalf("configuration revert stranded draining worker: %v %v", again, err)
						}
						ready, err := state.WithWorkerDeploymentActivation(ctx, d, i.ID)
						if err != nil {
							t.Fatal(err)
						}
						if err := s.MarkDeploymentLive(ready, d.ID); !errors.Is(err, state.ErrConflict) {
							t.Fatalf("draining worker became ready after configuration reverted: %v", err)
						}
					} else if claimed || after.State != before.State {
						t.Fatalf("claim crossed readiness/ownership: claimed=%v before=%s after=%s", claimed, before.State, after.State)
					}
				})
			}
		})
	}
}
