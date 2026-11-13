package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func primeCaptureFixture(t *testing.T, s state.Store) (state.App, state.Deployment, state.Instance, state.RuntimeInstancePublication) {
	t.Helper()
	ctx := t.Context()
	app, d := deploymentAttemptFixture(t, s)
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeRequest
	var err error
	app, err = s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInstanceWithMode(ctx, app.ID, d.ID, "cold_booting", 128, node.ID, uuid.NewString(), "normal")
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	p := state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: i.ID, NodeID: i.NodeID, WakeID: i.WakeID, ExpectedState: "cold_booting", Netns: "prime-capture", HostIP: "10.0.0.1", GuestUID: 20001, Fence: fence.SecretFence, ConfigFence: fence}
	i, err = s.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateWithTimestamp(ctx, i.ID, "snapshotting", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	i, err = s.InstanceByID(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, d, i, p
}

func captureHandoffPayload(t *testing.T, i state.Instance) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"deployment_id": i.DeploymentID, "source_instance_id": i.ID, "source_started_at": i.StartedAt, "node_id": i.NodeID, "storage_key": "snap/retained-capture", "tier": "init"})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSnapshotPrimeHandoffCommitsWithParkedState(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			store := s.(state.SnapshotPrimeHandoffStore)
			for _, change := range []string{"current", "env-during-capture", "wrong-wake", "wrong-node", "stopped", "cancelled", "requeued", "requeued-retagged", "artifact", "bad-payload", "unguarded"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, d, i, _ := primeCaptureFixture(t, s)
					guard, err := state.WithSnapshotPrimeAttempt(ctx, d)
					if err != nil {
						t.Fatal(err)
					}
					payload := captureHandoffPayload(t, i)
					expected := i
					switch change {
					case "env-during-capture":
						err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new")
					case "wrong-wake":
						expected.WakeID = uuid.NewString()
					case "wrong-node":
						expected.NodeID = uuid.NewString()
					case "stopped":
						err = s.UpdateInstanceState(ctx, i.ID, "stopped")
					case "cancelled":
						_, _, err = s.CancelDeploymentTx(ctx, d.ID, "test", state.CancelReasonUser)
					case "requeued", "requeued-retagged":
						if err = s.MarkDeploymentSuperseded(ctx, d.ID); err == nil {
							d, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
						}
						if err == nil && change == "requeued-retagged" {
							guard, err = state.WithSnapshotPrimeAttempt(ctx, d)
						}
					case "artifact":
						err = s.SetDeploymentRootfs(ctx, d.ID, "/new/rootfs", "new/rootfs", 4096)
					case "bad-payload":
						payload = []byte(`{"deployment_id":"foreign"}`)
					case "unguarded":
						guard = ctx
					}
					if err != nil {
						t.Fatal(err)
					}
					before, err := s.InstanceByID(ctx, i.ID)
					if err != nil {
						t.Fatal(err)
					}
					err = store.CompleteSnapshotPrimeHandoff(guard, expected, "parked", db.NotifySnapshotWritten, payload)
					accepted := change == "current" || change == "env-during-capture"
					if accepted && err != nil || !accepted && err == nil {
						t.Fatalf("completion error=%v accepted=%t", err, accepted)
					}
					after, err := s.InstanceByID(ctx, i.ID)
					if err != nil {
						t.Fatal(err)
					}
					handoff, readErr := store.SnapshotPrimeHandoff(guard, app.AccountID, app.ID, i.ID)
					if !accepted {
						if after.State != before.State || !errors.Is(readErr, state.ErrNotFound) {
							t.Fatalf("refused completion mutated state/evidence: %s -> %s, %v", before.State, after.State, readErr)
						}
						return
					}
					var wantPayload, gotPayload any
					if err := json.Unmarshal(payload, &wantPayload); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(handoff.Payload, &gotPayload); err != nil && accepted {
						t.Fatal(err)
					}
					if after.State != "parked" || readErr != nil || handoff.Channel != db.NotifySnapshotWritten || !reflect.DeepEqual(gotPayload, wantPayload) {
						t.Fatalf("capture and handoff diverged: %s %+v %v", after.State, handoff, readErr)
					}
					values, err := s.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					fence, err := state.NewRuntimeAppConfigFence(values)
					if err != nil {
						t.Fatal(err)
					}
					if (handoff.Fence == fence) != (change == "current") {
						t.Fatal("capture lost its original configuration fence")
					}
					if err = s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
						t.Fatal(err)
					}
					d, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					replacement, err := state.WithSnapshotPrimeAttempt(ctx, d)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.SnapshotPrimeHandoff(replacement, app.AccountID, app.ID, i.ID); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("replacement inherited old handoff: %v", err)
					}
				})
			}
		})
	}
}

func TestSnapshotPrimeHandoffClearedByRuntimePublication(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			s := scopedRollbackStore(t, backend)
			app, d, i, p := primeCaptureFixture(t, s)
			store := s.(state.SnapshotPrimeHandoffStore)
			guard, err := state.WithSnapshotPrimeAttempt(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.CompleteSnapshotPrimeHandoff(guard, i, "parked", db.NotifySnapshotWritten, captureHandoffPayload(t, i)); err != nil {
				t.Fatal(err)
			}
			if err = s.UpdateInstanceState(ctx, i.ID, "cold_booting"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(guard, p); err != nil {
				t.Fatal(err)
			}
			if _, err = store.SnapshotPrimeHandoff(guard, app.AccountID, app.ID, i.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("new publication retained capture: %v", err)
			}
		})
	}
}

func TestSnapshotPrimeLockDoesNotOwnActivationLock(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			s := scopedRollbackStore(t, backend)
			_, d := deploymentAttemptFixture(t, s)
			store := s.(state.SnapshotPrimeHandoffStore)
			release, err := store.AcquireSnapshotPrimeLock(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer release(ctx)
			bounded, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			activation, err := s.(state.DeploymentActivationLocker).AcquireDeploymentActivationLock(bounded, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			activation(ctx)
			cancelled, stop := context.WithCancel(ctx)
			stop()
			if _, err = store.AcquireSnapshotPrimeLock(cancelled, d.ID); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled contender: %v", err)
			}
			release(ctx)
			again, err := store.AcquireSnapshotPrimeLock(bounded, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			again(ctx)
		})
	}
}
