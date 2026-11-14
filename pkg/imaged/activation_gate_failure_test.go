package imaged_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type activationGateFailureMem struct {
	MemStore *state.MemStore
	before   func(context.Context)
}

func (s *activationGateFailureMem) ListEdgeRulesForApp(context.Context, string) ([]state.EdgeRule, error) {
	return nil, errors.New("contract inputs temporarily unavailable")
}
func (s *activationGateFailureMem) SetDeploymentFailed(ctx context.Context, id, code, detail string) (state.Deployment, error) {
	s.before(ctx)
	return s.MemStore.SetDeploymentFailed(ctx, id, code, detail)
}

type activationGateFailurePg struct {
	PgStore *state.PgStore
	before  func(context.Context)
}

func (s *activationGateFailurePg) ListEdgeRulesForApp(context.Context, string) ([]state.EdgeRule, error) {
	return nil, errors.New("contract inputs temporarily unavailable")
}
func (s *activationGateFailurePg) SetDeploymentFailed(ctx context.Context, id, code, detail string) (state.Deployment, error) {
	s.before(ctx)
	return s.PgStore.SetDeploymentFailed(ctx, id, code, detail)
}

func activationGateReadyNotification(n *activationGateNotifier, mode string) db.Notification {
	if mode == api.ExecutionModeWorker {
		return db.Notification{Channel: db.NotifyDeploymentReady, Payload: n.readyPayload}
	}
	return db.Notification{Channel: db.NotifySnapshotWritten, Payload: n.snapshotPayload}
}

func activationGateProdDeployment(t *testing.T, s state.Store, app state.App) state.Deployment {
	t.Helper()
	ctx := t.Context()
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "prod", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendDeploymentStage(ctx, d.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestContractFailurePreservesNewRollback(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := activationGateStore(t, backend)
			for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeWorker} {
				t.Run(mode, func(t *testing.T) {
					for _, change := range []string{"unchanged", "cancelled", "requeued", "requeued-config"} {
						t.Run(change, func(t *testing.T) {
							t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
							ctx := t.Context()
							acct, err := s.CreateAccount(ctx, uuid.NewString()+"@activation-gate.test", api.PlanPro)
							if err != nil {
								t.Fatal(err)
							}
							app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "gate-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: mode}})
							if err != nil {
								t.Fatal(err)
							}
							target := activationGateProdDeployment(t, s, app)
							v, n := &activationGateVM{}, &activationGateNotifier{}
							e := activationGateEngine(t, s, v, n)
							h := imaged.New(s, n, nil, nil, "", t.TempDir(), activationGateLog())
							if err := e.Prime(ctx, app.ID, target.ID); err != nil {
								t.Fatal(err)
							}
							if err := h.HandleNotification(ctx, activationGateReadyNotification(n, mode)); err != nil {
								t.Fatal(err)
							}
							current := activationGateProdDeployment(t, s, app)
							if err := e.Prime(ctx, app.ID, current.ID); err != nil {
								t.Fatal(err)
							}
							if err := h.HandleNotification(ctx, activationGateReadyNotification(n, mode)); err != nil {
								t.Fatal(err)
							}
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
							if err := e.Prime(ctx, app.ID, target.ID); err != nil {
								t.Fatal(err)
							}
							ready := activationGateReadyNotification(n, mode)
							boots := v.boots
							var retried state.RollbackRecord
							boundaryCalls := 0
							before := func(ctx context.Context) {
								boundaryCalls++
								if change == "unchanged" {
									return
								}
								if change == "cancelled" {
									if _, _, err := s.CancelDeploymentTx(ctx, target.ID, "test", state.CancelReasonUser); err != nil {
										t.Fatal(err)
									}
									return
								}
								if err := (&release.Rollbacks{Store: s, Log: activationGateLog()}).Sweep(ctx, first.RequestedAt.Add(api.RollbackOutcomeTimeout+time.Second)); err != nil {
									t.Fatal(err)
								}
								if change == "requeued-config" {
									if err := s.UpsertAppEnvInScope(ctx, app.AccountID, app.ID, "prod", "VERSION", "new"); err != nil {
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
							if backend == "memory" {
								wrapped = statetest.NewMem(&activationGateFailureMem{MemStore: s.(*state.MemStore), before: before})
							} else {
								wrapped = statetest.NewPg(&activationGateFailurePg{PgStore: s.(*state.PgStore), before: before})
							}
							t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
							blocked := imaged.New(wrapped, n, nil, nil, "", t.TempDir(), activationGateLog())
							activationErr := blocked.HandleNotification(ctx, ready)
							retryStatus := "none"
							if retried.ID != "" {
								if err := (&release.Rollbacks{Store: s, Log: activationGateLog()}).Sweep(ctx, time.Now()); err != nil {
									t.Fatal(err)
								}
								row, err := outcomes.RollbackRecordByID(ctx, app.AccountID, app.ID, retried.ID)
								if err != nil {
									t.Fatal(err)
								}
								retryStatus = row.Status
							}
							got, err := s.DeploymentByID(ctx, target.ID)
							if err != nil {
								t.Fatal(err)
							}
							serving, err := s.DeploymentByID(ctx, current.ID)
							if err != nil {
								t.Fatal(err)
							}
							t.Logf("change=%s failure_boundary_calls=%d status=%s code=%s retry=%s serving=%s/%d extra_boots=%d activation_error=%v", change, boundaryCalls, got.Status, got.ErrorCode, retryStatus, serving.Status, serving.TrafficPercent, v.boots-boots, activationErr)
							if boundaryCalls != 1 {
								t.Fatal("contract-failure write boundary was not exercised")
							}
							if serving.Status != state.DeployLive || serving.TrafficPercent != 100 {
								t.Fatal("serving control was lost")
							}
							if change == "cancelled" {
								if got.Status != state.DeployCancelled {
									t.Fatal("cancellation control failed")
								}
								return
							}
							if retried.ID != "" {
								if activationErr != nil || retryStatus != state.RollbackStatusPreparing || got.Status != state.DeploySnapshotting || got.ErrorCode != "" || v.boots != boots {
									t.Fatalf("old failure consumed replacement: err=%v retry=%s deployment=%s code=%s", activationErr, retryStatus, got.Status, got.ErrorCode)
								}
								return
							}
							if activationErr == nil || got.ErrorCode != api.CodeCapacity {
								t.Fatal("contract failure did not persist")
							}
						})
					}
				})
			}
		})
	}
}
