package imaged_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type activationGateVM struct {
	sched.RoutedVMM
	boots int
}

func (v *activationGateVM) CreateColdBoot(_ context.Context, _ string, id string, _ sched.AppSpec) (*sched.WakeOutcome, error) {
	v.boots++
	return &sched.WakeOutcome{Instance: id, Netns: "activation-gate-ns", HostIP: "10.0.0.1", LeaseUID: 20001}, nil
}
func (v *activationGateVM) PauseAndSnapshot(context.Context, string, string, string, string, string, bool) (sched.SnapshotBytes, error) {
	return sched.SnapshotBytes{MemBytes: 128 << 20, VMStateBytes: 4096, StoredBytes: 1 << 20}, nil
}
func (v *activationGateVM) Destroy(context.Context, string, string) error { return nil }
func (v *activationGateVM) StopInstanceOnNode(context.Context, string, string, int32, int32) (*sched.StopInstanceOutcome, error) {
	return &sched.StopInstanceOutcome{}, nil
}

type activationGateNotifier struct {
	snapshotPayload string
	readyPayload    string
}

func (n *activationGateNotifier) Notify(_ context.Context, channel, payload string) error {
	if channel == db.NotifySnapshotWritten {
		n.snapshotPayload = payload
	}
	if channel == db.NotifyDeploymentReady {
		n.readyPayload = payload
	}
	return nil
}
func activationGateLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func activationGateStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "postgres" {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	}
	return state.NewMemStore()
}
func activationGateFixture(t *testing.T, s state.Store, mode string) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@activation-gate.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "gate-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: mode}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), Status: state.DeploySnapshotting})
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
func activationGateEngine(t *testing.T, s state.Store, v *activationGateVM, n *activationGateNotifier) *sched.Engine {
	t.Helper()
	e, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), v, n, "1.10.0", activationGateLog())
	if err != nil {
		t.Fatal(err)
	}
	if err = e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	return e
}
func activationGateDeploymentForApp(t *testing.T, s state.Store, app state.App) state.Deployment {
	t.Helper()
	ctx := t.Context()
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64), Status: state.DeploySnapshotting})
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
func TestEphemeralReadinessUsesCurrentAttempt(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := activationGateStore(t, backend)
			for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeWorker} {
				t.Run(mode, func(t *testing.T) {
					for _, change := range []string{"current", "environment", "requeued", "requeued-config"} {
						t.Run(change, func(t *testing.T) {
							ctx := t.Context()
							app, d := activationGateFixture(t, s, mode)
							if mode == api.ExecutionModeRequest {
								if err := s.UpsertAppSecretWithClassInScope(ctx, app.AccountID, app.ID, api.DefaultEnvScope, "TOKEN", "key", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
									t.Fatal(err)
								}
							}
							if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "old"); err != nil {
								t.Fatal(err)
							}
							v, n := &activationGateVM{}, &activationGateNotifier{}
							e := activationGateEngine(t, s, v, n)
							if err := e.Prime(ctx, app.ID, d.ID); err != nil {
								t.Fatal(err)
							}
							oldReady := n.readyPayload
							if oldReady == "" {
								t.Fatal("prime emitted no readiness")
							}
							rows, err := s.ListInstancesForApp(ctx, app.ID)
							if err != nil || len(rows) != 1 {
								t.Fatalf("initial rows %+v %v", rows, err)
							}
							old := rows[0]
							h := imaged.New(s, n, nil, nil, "", t.TempDir(), activationGateLog())
							var current state.Deployment
							if strings.HasPrefix(change, "requeued") {
								if err := h.HandleNotification(ctx, db.Notification{Channel: db.NotifyDeploymentReady, Payload: oldReady}); err != nil {
									t.Fatal(err)
								}
								current = activationGateDeploymentForApp(t, s, app)
								if err := e.Prime(ctx, app.ID, current.ID); err != nil {
									t.Fatal(err)
								}
								if err := h.HandleNotification(ctx, db.Notification{Channel: db.NotifyDeploymentReady, Payload: n.readyPayload}); err != nil {
									t.Fatal(err)
								}
							}
							if change == "environment" || change == "requeued-config" {
								if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new"); err != nil {
									t.Fatal(err)
								}
							}
							if strings.HasPrefix(change, "requeued") {
								d, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
								if err != nil {
									t.Fatal(err)
								}
							}
							values, err := s.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							fence, err := state.NewRuntimeAppConfigFence(values)
							if err != nil {
								t.Fatal(err)
							}
							proof, err := s.(state.RuntimeInstancePublicationStore).InstanceRuntimeConfigFence(ctx, app.AccountID, app.ID, old.ID)
							if err != nil {
								t.Fatal(err)
							}
							boots := v.boots
							activateErr := h.HandleNotification(ctx, db.Notification{Channel: db.NotifyDeploymentReady, Payload: oldReady})
							got, err := s.DeploymentByID(ctx, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							previousStatus := "none"
							if current.ID != "" {
								p, err := s.DeploymentByID(ctx, current.ID)
								if err != nil {
									t.Fatal(err)
								}
								previousStatus = string(p.Status)
							}
							t.Logf("mode=%s change=%s old_instance=%s proof_current=%v deployment=%s traffic=%d previous=%s extra_boots=%d activation_error=%v", mode, change, old.State, proof == fence, got.Status, got.TrafficPercent, previousStatus, v.boots-boots, activateErr)
							if change == "current" {
								if activateErr != nil || got.Status != state.DeployLive {
									t.Fatalf("healthy activation control failed: %v", activateErr)
								}
								return
							}
							if activateErr == nil || got.Status != state.DeploySnapshotting || v.boots != boots {
								t.Fatalf("stale readiness moved traffic or executed again: err=%v status=%s boots=%d", activateErr, got.Status, v.boots)
							}
							if current.ID != "" && previousStatus != string(state.DeployLive) {
								t.Fatal("stale readiness displaced serving release")
							}
							if mode == api.ExecutionModeRequest {
								if err := e.Prime(ctx, app.ID, d.ID); err != nil {
									t.Fatal(err)
								}
								if err := h.HandleNotification(ctx, db.Notification{Channel: db.NotifyDeploymentReady, Payload: n.readyPayload}); err != nil {
									t.Fatal(err)
								}
								fresh, err := s.DeploymentByID(ctx, d.ID)
								if err != nil || fresh.Status != state.DeployLive || v.boots != boots+1 {
									t.Fatalf("fresh readiness did not activate: %+v boots=%d err=%v", fresh, v.boots, err)
								}
							}
						})
					}
				})
			}
		})
	}
}
