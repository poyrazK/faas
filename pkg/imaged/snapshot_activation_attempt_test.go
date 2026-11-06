package imaged_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSnapshotActivationDoesNotConsumeReplacementRollbackAttempt(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := readyAttemptStore(t, backend)
			for _, change := range []string{"unchanged", "cancelled", "withdrawn", "config-changed", "requeued", "requeued-config"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, candidate := readyAttemptWorker(t, s)
					manifest := app.Manifest
					manifest.ExecutionMode = api.ExecutionModeRequest
					var err error
					app, err = s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
					if err != nil {
						t.Fatal(err)
					}
					// A historical release is being restored through a normal plain rollback.
					readyAttemptMarkLive(t, s, candidate)
					previous := readyAttemptDeployment(t, s, app, state.DeploySnapshotting)
					readyAttemptMarkLive(t, s, previous)
					candidate, err = s.PrepareDeploymentRollback(ctx, app.ID, candidate.ID)
					if err != nil {
						t.Fatal(err)
					}
					outcomes := s.(state.RollbackOutcomeStore)
					request := state.RollbackRecord{AccountID: app.AccountID, AppID: app.ID, Scope: candidate.Scope, TargetDeploymentID: candidate.ID, FromDeploymentID: previous.ID, Trigger: state.RollbackTriggerManual}
					recorded, err := outcomes.RecordRollbackRequest(ctx, request)
					if err != nil {
						t.Fatal(err)
					}
					i := activationAttemptSnapshotSource(t, s, app, candidate)
					var prepared state.Deployment
					smokeCalls := 0
					h := imaged.New(s, readyAttemptNotifier{}, nil, nil, "", t.TempDir(), readyAttemptLog()).WithHostingSmokeRequired(true).WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
						smokeCalls++
						return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: 200, Path: "/healthz"}, nil
					}).WithValidatorArtifactCheck(func(ctx context.Context, _ string, id string) error {
						if change == "unchanged" {
							return nil
						}
						if change == "config-changed" {
							return s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "new")
						}
						if change == "cancelled" {
							_, _, err := s.CancelDeploymentTx(ctx, id, "audit11", state.CancelReasonUser)
							return err
						}
						// Let the production outcome sweep withdraw attempt 1 after its timeout.
						if err := (release.Rollbacks{Store: s, Log: readyAttemptLog()}).Sweep(ctx, recorded.RequestedAt.Add(api.RollbackOutcomeTimeout+time.Second)); err != nil {
							return err
						}
						outcome, err := outcomes.RollbackRecordByID(ctx, app.AccountID, app.ID, recorded.ID)
						if err != nil || outcome.Status != state.RollbackStatusFailed || outcome.ErrorCode != "rollback_timeout" {
							t.Fatalf("rollback was not withdrawn by its normal timeout: %+v %v", outcome, err)
						}
						if change == "withdrawn" {
							return nil
						}
						if change == "requeued-config" {
							if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "new"); err != nil {
								return err
							}
						}
						prepared, err = s.PrepareDeploymentRollback(ctx, app.ID, id)
						if err != nil {
							return err
						}
						_, err = outcomes.RecordRollbackRequest(ctx, request)
						return err
					})
					payload, _ := json.Marshal(map[string]any{"deployment_id": candidate.ID, "source_instance_id": i.ID, "source_started_at": i.StartedAt, "storage_key": state.SnapshotCaptureMemKey(candidate.ID, state.SnapshotTierInit, uuid.NewString()), "mem_bytes": int64(app.RAMMB) << 20, "vmstate_bytes": 4096, "fc_version": "1.10.0"})
					err = h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: string(payload)})
					got, readErr := s.DeploymentByID(ctx, candidate.ID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					old, readErr := s.DeploymentByID(ctx, previous.ID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					snap, snapErr := s.LatestSnapshot(ctx, candidate.ID)
					t.Logf("change=%s activation=%v target=%s/%d previous=%s/%d new-stage=%s snapshot.created=%s snapshot.err=%v", change, err, got.Status, got.TrafficPercent, old.Status, old.TrafficPercent, prepared.StageState, snap.CreatedAt, snapErr)
					if change == "unchanged" {
						if err != nil || got.Status != state.DeployLive {
							t.Errorf("valid snapshot activation failed: %v %s", err, got.Status)
						}
					} else if change == "cancelled" {
						if got.Status != state.DeployCancelled || old.Status != state.DeployLive {
							t.Error("stale activation replaced cancellation")
						}
					} else if change == "withdrawn" {
						if got.Status != state.DeploySuperseded || got.TrafficPercent != 0 || old.Status != state.DeployLive || old.TrafficPercent != 100 {
							t.Error("withdrawn rollback was promoted after its timeout")
						}
					} else if got.Status != state.DeploySnapshotting || old.Status != state.DeployLive || got.TrafficPercent != 0 || old.TrafficPercent != 100 {
						t.Error("old verified snapshot activation promoted a newly prepared attempt")
					}
					if change == "requeued" || change == "requeued-config" {
						// An outbox redelivery must not attach the old capture to the new attempt.
						if err := h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: string(payload)}); err != nil {
							t.Fatalf("retire old capture redelivery: %v", err)
						}
						d, err := s.DeploymentByID(ctx, candidate.ID)
						if err != nil || d.Status != state.DeploySnapshotting || d.TrafficPercent != 0 || smokeCalls != 1 {
							t.Fatalf("old capture was reverified for new attempt: status=%s traffic=%d smoke=%d err=%v", d.Status, d.TrafficPercent, smokeCalls, err)
						}
						// Fresh work for the replacement attempt must still activate normally.
						fresh := activationAttemptSnapshotSource(t, s, app, prepared)
						freshPayload, _ := json.Marshal(map[string]any{"deployment_id": candidate.ID, "source_instance_id": fresh.ID, "source_started_at": fresh.StartedAt, "storage_key": state.SnapshotCaptureMemKey(candidate.ID, state.SnapshotTierInit, uuid.NewString()), "mem_bytes": int64(app.RAMMB) << 20, "vmstate_bytes": 4096, "fc_version": "1.10.0"})
						next := imaged.New(s, readyAttemptNotifier{}, nil, nil, "", t.TempDir(), readyAttemptLog()).WithHostingSmokeRequired(true).WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
							smokeCalls++
							return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: 200, Path: "/healthz"}, nil
						})
						if err := next.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: string(freshPayload)}); err != nil {
							t.Fatal(err)
						}
						d, err = s.DeploymentByID(ctx, candidate.ID)
						if err != nil || d.Status != state.DeployLive || d.TrafficPercent != 100 || smokeCalls != 2 {
							t.Fatalf("fresh attempt failed to activate: status=%s traffic=%d smoke=%d err=%v", d.Status, d.TrafficPercent, smokeCalls, err)
						}

					}
				})
			}
		})
	}
}

func activationAttemptSnapshotSource(t *testing.T, s state.Store, app state.App, d state.Deployment) state.Instance {
	t.Helper()
	ctx := t.Context()
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.CreateInstanceWithMode(ctx, app.ID, d.ID, "cold_booting", app.RAMMB, node.ID, uuid.NewString(), "normal")
	if err != nil {
		t.Fatal(err)
	}
	values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		t.Fatal(err)
	}
	i, err = s.(state.RuntimeInstancePublicationStore).PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: app.AccountID, AppID: app.ID, InstanceID: i.ID, NodeID: i.NodeID, WakeID: i.WakeID, ExpectedState: "cold_booting", Netns: "audit11-snapshot", HostIP: "10.0.0.1", GuestUID: 20001, Fence: fence.SecretFence, ConfigFence: fence})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateInstanceState(ctx, i.ID, "snapshotting"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateInstanceState(ctx, i.ID, "parked"); err != nil {
		t.Fatal(err)
	}
	return i
}
