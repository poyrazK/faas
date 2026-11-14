package imaged_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
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

type readyAttemptNotifier struct{}

func (readyAttemptNotifier) Notify(context.Context, string, string) error { return nil }

type readyAttemptVM struct {
	sched.RoutedVMM
	mu         sync.Mutex
	instanceID string
	envVersion string
}

func (v *readyAttemptVM) CreateColdBoot(_ context.Context, _ string, id string, spec sched.AppSpec) (*sched.WakeOutcome, error) {
	v.mu.Lock()
	v.instanceID = id
	for _, entry := range spec.APIEnv {
		if entry.Key == "AUDIT_VERSION" {
			v.envVersion = entry.Value
		}
	}
	v.mu.Unlock()
	return &sched.WakeOutcome{Instance: id, Netns: "readyAttempt-ns", HostIP: "10.0.0.1", LeaseUID: 20001}, nil
}
func (v *readyAttemptVM) Destroy(context.Context, string, string) error {
	return nil
}
func (v *readyAttemptVM) StopInstanceOnNode(context.Context, string, string, int32, int32) (*sched.StopInstanceOutcome, error) {
	return &sched.StopInstanceOutcome{}, nil
}
func readyAttemptLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func readyAttemptStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "postgres" {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	}
	return state.NewMemStore()
}
func readyAttemptEngine(t *testing.T, s state.Store, v *readyAttemptVM) *sched.Engine {
	t.Helper()
	e, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), v, readyAttemptNotifier{}, "1.10.0", readyAttemptLog())
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func readyAttemptWorker(t *testing.T, s state.Store) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@readyAttempt10.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "readyAttempt10-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	d := readyAttemptDeployment(t, s, app, state.DeploySnapshotting)
	return app, d
}
func readyAttemptDeployment(t *testing.T, s state.Store, app state.App, status state.DeploymentStatus) state.Deployment {
	t.Helper()
	ctx := t.Context()
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), Status: status})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateDeploymentStatus(ctx, d.ID, status, ""); err != nil {
		t.Fatal(err)
	}
	if status == state.DeploySnapshotting {
		if _, err = s.AppendDeploymentStage(ctx, d.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
			t.Fatal(err)
		}
	}
	d, err = s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func readyAttemptMarkLive(t *testing.T, s state.Store, d state.Deployment) {
	t.Helper()
	if err := s.MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
}

func readyAttemptPrime(t *testing.T, s state.Store, app state.App, d state.Deployment) (state.Instance, string) {
	t.Helper()
	ctx := t.Context()
	v := &readyAttemptVM{}
	e := readyAttemptEngine(t, s, v)
	if err := e.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Prime(ctx, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	id, version := v.instanceID, v.envVersion
	v.mu.Unlock()
	i, err := s.InstanceByID(ctx, id)
	if err != nil || i.State != "running" {
		t.Fatalf("prime did not publish running worker: %+v %v", i, err)
	}
	return i, version
}
func readyAttemptActivate(t *testing.T, s state.Store, d state.Deployment, i state.Instance) error {
	t.Helper()
	h := imaged.New(s, readyAttemptNotifier{}, nil, nil, "", t.TempDir(), readyAttemptLog())
	payload, _ := json.Marshal(map[string]string{"deployment_id": d.ID, "execution_mode": "worker", "instance_id": i.ID})
	return h.HandleNotification(t.Context(), db.Notification{Channel: db.NotifyDeploymentReady, Payload: string(payload)})
}

func TestWorkerReadinessRequiresCurrentAttemptAndConfiguration(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := readyAttemptStore(t, backend)
			for _, proof := range []string{"old-running", "old-running-same-config", "fresh-running", "fresh-running-stale-config", "old-stopped"} {
				t.Run(proof, func(t *testing.T) {
					ctx := t.Context()
					app, d := readyAttemptWorker(t, s)
					if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "old"); err != nil {
						t.Fatal(err)
					}
					ins, version := readyAttemptPrime(t, s, app, d)
					if version != "old" {
						t.Fatalf("old boot did not use old values: %q", version)
					}
					if err := readyAttemptActivate(t, s, d, ins); err != nil {
						t.Fatal(err)
					}
					next := readyAttemptDeployment(t, s, app, state.DeploySnapshotting)
					readyAttemptMarkLive(t, s, next)
					if proof != "old-running-same-config" {
						if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "new"); err != nil {
							t.Fatal(err)
						}
					}
					prepared, err := s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					if proof == "fresh-running" || proof == "fresh-running-stale-config" {
						ins, version = readyAttemptPrime(t, s, app, d)
						if version != "new" {
							t.Fatalf("fresh boot did not use new values: %q", version)
						}
					}
					if proof == "fresh-running-stale-config" {
						if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "AUDIT_VERSION", "newer"); err != nil {
							t.Fatal(err)
						}
					}
					if proof == "old-stopped" {
						if err = s.UpdateInstanceState(ctx, ins.ID, "stopped"); err != nil {
							t.Fatal(err)
						}
					}
					storedFence, fenceErr := s.(state.RuntimeInstancePublicationStore).InstanceRuntimeConfigFence(ctx, app.AccountID, app.ID, ins.ID)
					if fenceErr != nil {
						t.Fatal(fenceErr)
					}
					values, fenceErr := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
					if fenceErr != nil {
						t.Fatal(fenceErr)
					}
					currentFence, fenceErr := state.NewRuntimeAppConfigFence(values)
					if fenceErr != nil {
						t.Fatal(fenceErr)
					}
					fenceMatches := storedFence.Fingerprint == currentFence.Fingerprint
					if fenceMatches != (proof == "fresh-running" || proof == "old-running-same-config") {
						t.Fatalf("bad fixture: current runtime proof matches=%v", fenceMatches)
					}
					err = readyAttemptActivate(t, s, d, ins)
					got, readErr := s.DeploymentByID(ctx, d.ID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					current, readErr := s.DeploymentByID(ctx, next.ID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					t.Logf("proof=%s boot.env=%s config.matches=%v instance.started=%s rollback.stage=%s activation.err=%v target=%s/%d current=%s/%d", proof, version, fenceMatches, ins.StartedAt, prepared.StageState, err, got.Status, got.TrafficPercent, current.Status, current.TrafficPercent)
					if proof == "fresh-running" {
						if err != nil || got.Status != state.DeployLive {
							t.Errorf("fresh readiness refused: %v status=%s", err, got.Status)
						}
						if err := readyAttemptActivate(t, s, d, ins); err != nil {
							t.Fatalf("completed readiness replay failed: %v", err)
						}
					} else if err == nil || got.Status != state.DeploySnapshotting || current.Status != state.DeployLive {
						t.Errorf("stale readiness activated rollback without a fresh boot: err=%v target=%s current=%s", err, got.Status, current.Status)
					}
				})
			}
		})
	}
}
