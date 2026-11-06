package sched_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type primeHandoffVM struct {
	sched.RoutedVMM
	boots, captures, destroys int
	bootErr                   error
	captureErr                error
	bootHook                  func()
}

func (v *primeHandoffVM) CreateColdBoot(_ context.Context, _ string, id string, _ sched.AppSpec) (*sched.WakeOutcome, error) {
	v.boots++
	if v.bootHook != nil {
		v.bootHook()
	}
	if v.bootErr != nil {
		return nil, v.bootErr
	}
	return &sched.WakeOutcome{Instance: id, Netns: "audit13-ns", HostIP: "10.0.0.1", LeaseUID: 20001}, nil
}
func (v *primeHandoffVM) PauseAndSnapshot(context.Context, string, string, string, string, string, bool) (sched.SnapshotBytes, error) {
	v.captures++
	if v.captureErr != nil {
		return sched.SnapshotBytes{}, v.captureErr
	}
	return sched.SnapshotBytes{MemBytes: 128 << 20, VMStateBytes: 4096, StoredBytes: 1 << 20}, nil
}
func (v *primeHandoffVM) Destroy(context.Context, string, string) error { v.destroys++; return nil }
func (v *primeHandoffVM) StopInstanceOnNode(context.Context, string, string, int32, int32) (*sched.StopInstanceOutcome, error) {
	return &sched.StopInstanceOutcome{}, nil
}

type primeHandoffNotifier struct {
	failSnapshot                       bool
	failReady                          bool
	crashReady                         bool
	crashRunning                       bool
	snapshotAttempts, snapshots, ready int
}

func (n *primeHandoffNotifier) Notify(_ context.Context, channel, payload string) error {
	if channel == db.NotifyInstanceChanged && n.crashRunning {
		var event struct {
			State string `json:"state"`
		}
		if json.Unmarshal([]byte(payload), &event) == nil && event.State == "running" {
			panic("scheduler crashed after durable runtime publication")
		}
	}
	if channel == db.NotifySnapshotWritten {
		n.snapshotAttempts++
		if n.failSnapshot {
			return errors.New("snapshot handoff transaction unavailable")
		}
		n.snapshots++
	}
	if channel == db.NotifyDeploymentReady {
		if n.crashReady {
			panic("audit: scheduler crashed before readiness enqueue")
		}
		if n.failReady {
			return errors.New("readiness handoff transaction unavailable")
		}
		n.ready++
	}
	return nil
}
func primeHandoffLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func primeHandoffStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "postgres" {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	}
	return state.NewMemStore()
}
func primeHandoffFixture(t *testing.T, s state.Store, mode string) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@audit13.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "a13-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, Manifest: state.AppManifest{ExecutionMode: mode}})
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
func primeHandoffEngine(t *testing.T, s state.Store, v *primeHandoffVM, n *primeHandoffNotifier) *sched.Engine {
	t.Helper()
	e, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), v, n, "1.10.0", primeHandoffLog())
	if err != nil {
		t.Fatal(err)
	}
	if err = e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	return e
}
func crashPrimeHandoff(t *testing.T, e *sched.Engine, app, dep string) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Error("crash window was not exercised")
		}
	}()
	if err := e.Prime(t.Context(), app, dep); err != nil {
		t.Fatal(err)
	}
}

func TestPrimeActivationHandoffRecoversAfterRestart(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := primeHandoffStore(t, backend)
			for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeWorker} {
				t.Run(mode, func(t *testing.T) {
					for _, handoff := range []string{"missing", "healthy", "stopped"} {
						t.Run(handoff, func(t *testing.T) {
							ctx := t.Context()
							app, d := primeHandoffFixture(t, s, mode)
							v, n := &primeHandoffVM{}, &primeHandoffNotifier{failSnapshot: handoff != "healthy", crashReady: handoff != "healthy" && mode == api.ExecutionModeWorker}
							first := primeHandoffEngine(t, s, v, n)
							if n.crashReady {
								crashPrimeHandoff(t, first, app.ID, d.ID)
							} else {
								initialLoop := sched.NewLoop(nil, first, primeHandoffLog())
								payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": d.ID})
								if err := initialLoop.HandleDurableNotification(ctx, db.Notification{Channel: db.NotifySnapshotPrime, Payload: string(payload)}); err != nil {
									t.Fatal(err)
								}
								initialLoop.WaitPrimes()
							}
							rows, err := s.ListInstancesForApp(ctx, app.ID)
							if err != nil || len(rows) != 1 {
								t.Fatalf("post-prime instances=%+v err=%v", rows, err)
							}
							stateAfter := rows[0].State
							if handoff == "stopped" {
								if err := s.UpdateInstanceState(ctx, rows[0].ID, "stopped"); err != nil {
									t.Fatal(err)
								}
							}
							n.failSnapshot, n.crashReady = false, false
							restarted := primeHandoffEngine(t, s, v, n)
							loop := sched.NewLoop(nil, restarted, primeHandoffLog())
							for range 3 {
								if err := loop.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err != nil {
									t.Fatal(err)
								}
								loop.WaitPrimes()
							}
							got, err := s.DeploymentByID(ctx, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							_, snapErr := s.LatestSnapshot(ctx, d.ID)
							t.Logf("mode=%s state=%s deployment=%s captures=%d boots=%d snapshot-attempts=%d queued-snapshots=%d queued-ready=%d snapshot-row-error=%v", mode, stateAfter, got.Status, v.captures, v.boots, n.snapshotAttempts, n.snapshots, n.ready, snapErr)
							if n.snapshots+n.ready == 0 {
								t.Error("healthy recovery never recreated the missing activation handoff")
							}
							wantBoots := 1
							if handoff == "stopped" && mode == api.ExecutionModeWorker {
								wantBoots = 2
							}
							if v.boots != wantBoots || got.Status != state.DeploySnapshotting {
								t.Fatalf("handoff replay executed again or failed deployment: boots=%d status=%s", v.boots, got.Status)
							}
						})
					}
				})
			}
		})
	}
}

func TestPrimeActivationReplayBeforeReadinessDoesNotReexecute(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := primeHandoffStore(t, backend)
			for _, mode := range []string{api.ExecutionModeWorker, api.ExecutionModeRequest} {
				t.Run(mode, func(t *testing.T) {
					for _, phase := range []string{"pending", "readiness", "readiness-complete", "cancelled", "completed", "duplicate-failure"} {
						if phase == "completed" && mode != api.ExecutionModeWorker {
							continue
						}
						t.Run(phase, func(t *testing.T) {
							ctx := t.Context()
							app, d := primeHandoffFixture(t, s, mode)
							v, n := &primeHandoffVM{}, &primeHandoffNotifier{}
							e := primeHandoffEngine(t, s, v, n)
							loop := sched.NewLoop(nil, e, primeHandoffLog())
							payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": d.ID})
							for delivery := range 3 {
								if err := loop.HandleDurableNotification(ctx, db.Notification{Channel: db.NotifySnapshotPrime, Payload: string(payload)}); err != nil {
									t.Fatal(err)
								}
								loop.WaitPrimes()
								if delivery == 0 {
									switch phase {
									case "readiness", "readiness-complete":
										if _, err := s.AppendDeploymentStage(ctx, d.ID, state.StageSnapshotPrepare, state.StageReadiness, time.Now().UTC(), ""); err != nil {
											t.Fatal(err)
										}
										if phase == "readiness-complete" {
											if _, err := s.CloseDeploymentStage(ctx, d.ID, state.StageReadiness, time.Now().UTC()); err != nil {
												t.Fatal(err)
											}
										}
									case "cancelled":
										if _, _, err := s.CancelDeploymentTx(ctx, d.ID, "audit13", state.CancelReasonUser); err != nil {
											t.Fatal(err)
										}
									case "completed":
										rows, err := s.ListInstancesForApp(ctx, app.ID)
										if err != nil || len(rows) != 1 {
											t.Fatalf("activation rows: %+v %v", rows, err)
										}
										ready, _ := json.Marshal(map[string]string{"deployment_id": d.ID, "execution_mode": mode, "instance_id": rows[0].ID})
										h := imaged.New(s, n, nil, nil, "", t.TempDir(), primeHandoffLog())
										if err := h.HandleNotification(ctx, db.Notification{Channel: db.NotifyDeploymentReady, Payload: string(ready)}); err != nil {
											t.Fatal(err)
										}
									case "duplicate-failure":
										v.bootErr = errors.New("the duplicate prime fails to boot")
									}
								}
							}
							rows, err := s.ListInstancesForApp(ctx, app.ID)
							if err != nil {
								t.Fatal(err)
							}
							running := 0
							for _, i := range rows {
								if i.State == "running" {
									running++
								}
							}
							got, err := s.DeploymentByID(ctx, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							t.Logf("mode=%s max-concurrency=1 boots=%d captures=%d running=%d instances=%d queued-ready=%d queued-snapshot=%d deployment=%s", mode, v.boots, v.captures, running, len(rows), n.ready, n.snapshots, got.Status)
							want := state.DeploySnapshotting
							if phase == "cancelled" {
								want = state.DeployCancelled
							}
							if phase == "completed" {
								want = state.DeployLive
							}
							if v.boots != 1 || got.Status != want {
								t.Error("duplicate hint started another VM or failed a healthy candidate")
							}
						})
					}
				})
			}
		})
	}
}

func TestPrimeActivationReplayUsesCurrentAttemptAndInputs(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := primeHandoffStore(t, backend)
			for _, mode := range []string{api.ExecutionModeWorker, api.ExecutionModeRequest, api.ExecutionModeService} {
				t.Run(mode, func(t *testing.T) {
					for _, change := range []string{"environment", "requeued"} {
						t.Run(change, func(t *testing.T) {
							ctx := t.Context()
							app, d := primeHandoffFixture(t, s, mode)
							v, n := &primeHandoffVM{}, &primeHandoffNotifier{}
							e := primeHandoffEngine(t, s, v, n)
							if err := e.Prime(ctx, app.ID, d.ID); err != nil {
								t.Fatal(err)
							}
							if change == "environment" {
								if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new"); err != nil {
									t.Fatal(err)
								}
							} else {
								if err := s.MarkDeploymentSuperseded(ctx, d.ID); err != nil {
									t.Fatal(err)
								}
								var err error
								d, err = s.PrepareDeploymentRollback(ctx, app.ID, d.ID)
								if err != nil {
									t.Fatal(err)
								}
							}
							loop := sched.NewLoop(nil, e, primeHandoffLog())
							for range 3 {
								if err := loop.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err != nil {
									t.Fatal(err)
								}
								loop.WaitPrimes()
							}
							got, err := s.DeploymentByID(ctx, d.ID)
							if err != nil {
								t.Fatal(err)
							}
							if v.boots != 2 || got.Status != state.DeploySnapshotting {
								t.Fatalf("replacement blocked or duplicated: boots=%d status=%s", v.boots, got.Status)
							}
							rows, err := s.ListInstancesForApp(ctx, app.ID)
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
							fresh := false
							for _, i := range rows {
								proof, err := s.(state.RuntimeInstancePublicationStore).InstanceRuntimeConfigFence(ctx, app.AccountID, app.ID, i.ID)
								if err == nil && proof == fence {
									fresh = true
								}
							}
							if !fresh {
								t.Fatal("replacement did not publish the current configuration")
							}
						})
					}
				})
			}
		})
	}
}

func TestPrimeActivationSerializesIndependentEngines(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := primeHandoffStore(t, backend)
			for _, mode := range []string{api.ExecutionModeWorker, api.ExecutionModeRequest} {
				t.Run(mode, func(t *testing.T) {
					ctx := t.Context()
					app, d := primeHandoffFixture(t, s, mode)
					v, n := &primeHandoffVM{}, &primeHandoffNotifier{}
					entered, release := make(chan struct{}), make(chan struct{})
					var once sync.Once
					v.bootHook = func() { once.Do(func() { close(entered); <-release }) }
					first, second := primeHandoffEngine(t, s, v, n), primeHandoffEngine(t, s, v, n)
					results := make(chan error, 2)
					go func() { results <- first.Prime(ctx, app.ID, d.ID) }()
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("first prime did not boot")
					}
					started := make(chan struct{})
					go func() { close(started); results <- second.Prime(ctx, app.ID, d.ID) }()
					<-started
					close(release)
					for range 2 {
						if err := <-results; err != nil {
							t.Fatal(err)
						}
					}
					if v.boots != 1 {
						t.Fatalf("independent schedulers booted %d copies", v.boots)
					}
				})
			}
		})
	}
}

type lostPrimeCompletionMem struct {
	MemStore *state.MemStore
	lost     bool
}

func (s *lostPrimeCompletionMem) CompleteSnapshotPrimeHandoff(ctx context.Context, i state.Instance, next, channel string, payload json.RawMessage) error {
	if err := s.MemStore.CompleteSnapshotPrimeHandoff(ctx, i, next, channel, payload); err != nil {
		return err
	}
	if !s.lost {
		s.lost = true
		return errors.New("completion committed but acknowledgment was lost")
	}
	return nil
}

type lostPrimeCompletionPg struct {
	PgStore *state.PgStore
	lost    bool
}

func (s *lostPrimeCompletionPg) CompleteSnapshotPrimeHandoff(ctx context.Context, i state.Instance, next, channel string, payload json.RawMessage) error {
	if err := s.PgStore.CompleteSnapshotPrimeHandoff(ctx, i, next, channel, payload); err != nil {
		return err
	}
	if !s.lost {
		s.lost = true
		return errors.New("completion committed but acknowledgment was lost")
	}
	return nil
}

func TestPrimeActivationLostCommitAcknowledgmentResumesCapture(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			base := primeHandoffStore(t, backend)
			app, d := primeHandoffFixture(t, base, api.ExecutionModeRequest)
			var s state.Store
			if backend == "postgres" {
				s = statetest.NewPg(&lostPrimeCompletionPg{PgStore: base.(*state.PgStore)})
			} else {
				s = statetest.NewMem(&lostPrimeCompletionMem{MemStore: base.(*state.MemStore)})
			}
			v, n := &primeHandoffVM{}, &primeHandoffNotifier{}
			e := primeHandoffEngine(t, s, v, n)
			loop := sched.NewLoop(nil, e, primeHandoffLog())
			payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": d.ID})
			if err := loop.HandleDurableNotification(ctx, db.Notification{Channel: db.NotifySnapshotPrime, Payload: string(payload)}); err != nil {
				t.Fatal(err)
			}
			loop.WaitPrimes()
			before, err := s.DeploymentByID(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if before.Status != state.DeploySnapshotting || e.Ledger().ResidentRAM() == 0 || n.snapshots != 0 {
				t.Fatalf("ambiguous completion lost recovery/accounting: status=%s RAM=%d messages=%d", before.Status, e.Ledger().ResidentRAM(), n.snapshots)
			}
			if err := loop.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			loop.WaitPrimes()
			if v.boots != 1 || v.captures != 1 || n.snapshots != 1 || e.Ledger().ResidentRAM() != 0 {
				t.Fatalf("ambiguous completion reexecuted or leaked reservation: boots=%d captures=%d messages=%d RAM=%d", v.boots, v.captures, n.snapshots, e.Ledger().ResidentRAM())
			}
		})
	}
}

func TestPrimeActivationRecoversNoSnapshotHandoff(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := primeHandoffStore(t, backend)
			for _, mode := range []string{api.ExecutionModeRequest, api.ExecutionModeService, api.ExecutionModeJob} {
				t.Run(mode, func(t *testing.T) {
					ctx := t.Context()
					app, d := primeHandoffFixture(t, s, mode)
					if mode != api.ExecutionModeJob {
						if err := s.UpsertAppSecretWithClassInScope(ctx, app.AccountID, app.ID, api.DefaultEnvScope, "TOKEN", "key", "hash", state.SecretClassEphemeral, []byte("sealed")); err != nil {
							t.Fatal(err)
						}
					}
					v, n := &primeHandoffVM{}, &primeHandoffNotifier{failReady: true}
					e := primeHandoffEngine(t, s, v, n)
					loop := sched.NewLoop(nil, e, primeHandoffLog())
					payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": d.ID})
					if err := loop.HandleDurableNotification(ctx, db.Notification{Channel: db.NotifySnapshotPrime, Payload: string(payload)}); err != nil {
						t.Fatal(err)
					}
					loop.WaitPrimes()
					before, err := s.DeploymentByID(ctx, d.ID)
					if err != nil || before.Status != state.DeploySnapshotting {
						t.Fatalf("notification failure failed deployment: %s %v", before.Status, err)
					}
					n.failReady = false
					restarted := primeHandoffEngine(t, s, v, n)
					recovery := sched.NewLoop(nil, restarted, primeHandoffLog())
					if err := recovery.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err != nil {
						t.Fatal(err)
					}
					recovery.WaitPrimes()
					wantBoots, wantDestroys := 1, 1
					if mode == api.ExecutionModeJob {
						wantBoots, wantDestroys = 0, 0
					}
					if v.boots != wantBoots || v.captures != 0 || v.destroys != wantDestroys || n.ready != 1 {
						t.Fatalf("no-snapshot recovery effects: boots=%d captures=%d destroys=%d ready=%d", v.boots, v.captures, v.destroys, n.ready)
					}
				})
			}
		})
	}
}
