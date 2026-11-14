package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func deploymentAttemptFixture(t *testing.T, s state.Store) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@attempt-guard.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "attempt-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendDeploymentStage(ctx, d.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, d
}

func TestPrimeFailureWriteRechecksAttemptAtomically(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, change := range []string{"unchanged", "requeued", "artifact", "other-failure"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, d := deploymentAttemptFixture(t, s)
					primeCtx, err := state.WithSnapshotPrimeAttempt(ctx, d)
					if err != nil {
						t.Fatal(err)
					}
					cause := state.SnapshotPrimeAttemptError(primeCtx, errors.New("old boot failed"))
					failureCtx, err := state.WithSnapshotPrimeFailure(ctx, cause, d)
					if err != nil {
						t.Fatal(err)
					}
					switch change {
					case "requeued":
						if err = s.MarkDeploymentSuperseded(ctx, d.ID); err == nil {
							_, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
						}
					case "artifact":
						err = s.SetDeploymentRootfs(ctx, d.ID, "/new", "new/key", 4096)
					case "other-failure":
						_, err = s.SetDeploymentFailedEx(ctx, d.ID, api.CodeDeployFailed, "independent failure", "", "", "", nil)
					}
					if err != nil {
						t.Fatal(err)
					}
					before, err := s.DeploymentByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					_, err = s.SetDeploymentFailedEx(failureCtx, d.ID, api.CodeDeployFailed, "old boot failed", "", "", "", nil)
					if change == "unchanged" {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						if !errors.Is(err, state.ErrSnapshotPrimeStale) {
							t.Fatalf("stale failure write=%v", err)
						}
						after, readErr := s.DeploymentByID(ctx, d.ID)
						if readErr != nil {
							t.Fatal(readErr)
						}
						if before.Status != after.Status || before.Error != after.Error || string(before.StageState) != string(after.StageState) {
							t.Fatal("rejected failure modified its replacement")
						}
					}
				})
			}
		})
	}
}

func publishAttemptWorker(t *testing.T, s state.Store, app state.App, d state.Deployment, proof bool) state.Instance {
	t.Helper()
	ctx := t.Context()
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInstanceWithMode(ctx, app.ID, d.ID, "cold_booting", app.RAMMB, node.ID, uuid.NewString(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	if proof {
		values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		fence, err := state.NewRuntimeAppConfigFence(values)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: i.ID, NodeID: i.NodeID, WakeID: i.WakeID, ExpectedState: "cold_booting", Netns: "attempt-test", HostIP: "10.0.0.1", GuestUID: 20001, Fence: fence.SecretFence, ConfigFence: fence})
		if err != nil {
			t.Fatal(err)
		}
	} else if err = s.UpdateInstanceState(ctx, i.ID, "running"); err != nil {
		t.Fatal(err)
	}
	i, err = s.InstanceByID(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestWorkerActivationWriteRechecksReadinessAtomically(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, change := range []string{"unchanged", "env", "stopped", "requeued", "missing-proof"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, d := deploymentAttemptFixture(t, s)
					if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
						t.Fatal(err)
					}
					current, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64)})
					if err != nil {
						t.Fatal(err)
					}
					if err = s.MarkDeploymentLive(ctx, current.ID); err != nil {
						t.Fatal(err)
					}
					d, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					i := publishAttemptWorker(t, s, app, d, change != "missing-proof")
					guarded, err := state.WithWorkerDeploymentActivation(ctx, d, i.ID)
					if err != nil {
						t.Fatal(err)
					}
					switch change {
					case "env":
						err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "CHANGED", "new")
					case "stopped":
						err = s.UpdateInstanceState(ctx, i.ID, "stopped")
					case "requeued":
						if err = s.MarkDeploymentSuperseded(ctx, d.ID); err == nil {
							_, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					err = s.MarkDeploymentLive(state.WithPreparedRollbackPromotion(guarded), d.ID)
					if change == "unchanged" {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						if !errors.Is(err, state.ErrConflict) {
							t.Fatalf("stale activation=%v", err)
						}
						target, _ := s.DeploymentByID(ctx, d.ID)
						old, _ := s.DeploymentByID(ctx, current.ID)
						if target.Status != state.DeploySnapshotting || target.TrafficPercent != 0 || old.Status != state.DeployLive || old.TrafficPercent != 100 {
							t.Fatalf("rejected activation moved traffic: target=%s/%d current=%s/%d", target.Status, target.TrafficPercent, old.Status, old.TrafficPercent)
						}
					}
				})
			}
		})
	}
}
