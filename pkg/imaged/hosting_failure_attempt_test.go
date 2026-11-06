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
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type staleHostingMemBoundary struct {
	MemStore *state.MemStore
	before   func(context.Context)
	calls    int
}

func (s *staleHostingMemBoundary) FailDeploymentWithHostingReceipt(ctx context.Context, id string, raw []byte, code, message string) (bool, error) {
	s.calls++
	s.before(ctx)
	return s.MemStore.FailDeploymentWithHostingReceipt(ctx, id, raw, code, message)
}

type staleHostingPgBoundary struct {
	PgStore *state.PgStore
	before  func(context.Context)
	calls   int
}

func (s *staleHostingPgBoundary) FailDeploymentWithHostingReceipt(ctx context.Context, id string, raw []byte, code, message string) (bool, error) {
	s.calls++
	s.before(ctx)
	return s.PgStore.FailDeploymentWithHostingReceipt(ctx, id, raw, code, message)
}

func TestDelayedHostingFailureDoesNotConsumeReplacementAttempt(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := readyAttemptStore(t, backend)
			for _, change := range []string{"unchanged", "cancelled", "withdrawn", "config-changed", "requeued", "requeued-config"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, target := readyAttemptWorker(t, s)
					manifest := app.Manifest
					manifest.ExecutionMode = api.ExecutionModeRequest
					var err error
					app, err = s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
					if err != nil {
						t.Fatal(err)
					}
					readyAttemptMarkLive(t, s, target)
					current := readyAttemptDeployment(t, s, app, state.DeploySnapshotting)
					readyAttemptMarkLive(t, s, current)
					target, err = s.PrepareDeploymentRollback(ctx, app.ID, target.ID)
					if err != nil {
						t.Fatal(err)
					}
					outcomes := s.(state.RollbackOutcomeStore)
					request := state.RollbackRecord{AccountID: app.AccountID, AppID: app.ID, Scope: target.Scope, TargetDeploymentID: target.ID, FromDeploymentID: current.ID, Trigger: state.RollbackTriggerManual}
					first, err := outcomes.RecordRollbackRequest(ctx, request)
					if err != nil {
						t.Fatal(err)
					}
					i := activationAttemptSnapshotSource(t, s, app, target)
					var retried state.RollbackRecord
					before := func(ctx context.Context) {
						if change == "unchanged" {
							return
						}
						if change == "config-changed" {
							if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "new"); err != nil {
								t.Fatal(err)
							}
							return
						}
						if change == "cancelled" {
							if _, _, err := s.CancelDeploymentTx(ctx, target.ID, "audit12", state.CancelReasonUser); err != nil {
								t.Fatal(err)
							}
							return
						}
						if err := (release.Rollbacks{Store: s, Log: readyAttemptLog()}).Sweep(ctx, first.RequestedAt.Add(api.RollbackOutcomeTimeout+time.Second)); err != nil {
							t.Fatal(err)
						}
						if change == "withdrawn" {
							return
						}
						if change == "requeued-config" {
							if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "new"); err != nil {
								t.Fatal(err)
							}
						}
						if _, err := s.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
							t.Fatal(err)
						}
						var err error
						retried, err = outcomes.RecordRollbackRequest(ctx, request)
						if err != nil {
							t.Fatal(err)
						}
					}
					var wrapped state.Store
					if mem, ok := s.(*state.MemStore); ok {
						wrapped = statetest.NewMem(&staleHostingMemBoundary{MemStore: mem, before: before})
					} else {
						wrapped = statetest.NewPg(&staleHostingPgBoundary{PgStore: s.(*state.PgStore), before: before})
					}
					h := imaged.New(wrapped, readyAttemptNotifier{}, nil, nil, "", t.TempDir(), readyAttemptLog()).WithHostingSmokeRequired(true).WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
						return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed, StatusCode: 503, Path: "/healthz"}, nil
					})
					payload, _ := json.Marshal(map[string]any{"deployment_id": target.ID, "source_instance_id": i.ID, "source_started_at": i.StartedAt, "storage_key": state.SnapshotCaptureMemKey(target.ID, state.SnapshotTierInit, uuid.NewString()), "mem_bytes": int64(app.RAMMB) << 20, "vmstate_bytes": 4096, "fc_version": "1.10.0"})
					activationErr := h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: string(payload)})
					d, err := s.DeploymentByID(ctx, target.ID)
					if err != nil {
						t.Fatal(err)
					}
					serving, err := s.DeploymentByID(ctx, current.ID)
					if err != nil {
						t.Fatal(err)
					}
					newStatus := ""
					if retried.ID != "" {
						if err := (release.Rollbacks{Store: s, Log: readyAttemptLog()}).Sweep(ctx, time.Now().UTC()); err != nil {
							t.Fatal(err)
						}
						latest, err := outcomes.RollbackRecordByID(ctx, app.AccountID, app.ID, retried.ID)
						if err != nil {
							t.Fatal(err)
						}
						newStatus = latest.Status
					}
					t.Logf("change=%s activation=%v candidate=%s/%d code=%s current=%s/%d retry.status=%s receipt.bytes=%d", change, activationErr, d.Status, d.TrafficPercent, d.ErrorCode, serving.Status, serving.TrafficPercent, newStatus, len(d.APIHostingReceipt))
					switch change {
					case "unchanged":
						if activationErr == nil || d.ErrorCode != api.CodeDeploymentSmokeFailed {
							t.Error("current failed smoke was not committed")
						}
					case "cancelled":
						if d.Status != state.DeployCancelled {
							t.Error("cancelled attempt changed")
						}
					case "withdrawn":
						if d.Status != state.DeploySuperseded || d.ErrorCode == api.CodeDeploymentSmokeFailed {
							t.Error("withdrawn attempt received old failure")
						}
					default:
						if d.Status != state.DeploySnapshotting || d.ErrorCode != "" || (retried.ID != "" && newStatus != state.RollbackStatusPreparing) || len(d.APIHostingReceipt) > 2 {
							t.Error("old smoke failure finalized the newly requested rollback")
						}
					}
					if serving.Status != state.DeployLive || serving.TrafficPercent != 100 {
						t.Error("lost serving predecessor")
					}
				})
			}
		})
	}
}
