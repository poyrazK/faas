// adr: 208
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type rolloutRecoveryNotifier struct{}

func (rolloutRecoveryNotifier) Notify(context.Context, string, string) error { return nil }

type rolloutRecoveryVM struct {
	sched.RoutedVMM
	mu           sync.Mutex
	boots, parks int
}

func (v *rolloutRecoveryVM) CreateColdBoot(context.Context, string, string, sched.AppSpec) (*sched.WakeOutcome, error) {
	v.mu.Lock()
	v.boots++
	v.mu.Unlock()
	return nil, errors.New("audit: replacement boot unavailable")
}
func (v *rolloutRecoveryVM) PauseAndSnapshot(context.Context, string, string, string, string, string, bool) (sched.SnapshotBytes, error) {
	v.mu.Lock()
	v.parks++
	v.mu.Unlock()
	return sched.SnapshotBytes{MemBytes: 130 << 20, VMStateBytes: 4096}, nil
}
func (v *rolloutRecoveryVM) WarmSnapshot(context.Context, string, string, string, string) (sched.SnapshotBytes, error) {
	return sched.SnapshotBytes{}, nil
}
func (v *rolloutRecoveryVM) Destroy(context.Context, string, string) error { return nil }
func (v *rolloutRecoveryVM) counts() (int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.boots, v.parks
}

type rolloutRecoveryFixture struct {
	store     state.Store
	app       state.App
	node      state.ComputeNode
	old, next state.Deployment
	vm        *rolloutRecoveryVM
	engine    *sched.Engine
}

func rolloutRecoveryStore(t *testing.T, backend string) state.Store {
	t.Helper()
	if backend == "postgres" {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	}
	return state.NewMemStore()
}
func newRolloutRecoveryFixture(t *testing.T, s state.Store, desired int, probe bool) rolloutRecoveryFixture {
	t.Helper()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, "audit8-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "service-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: desired}}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(candidate bool) state.Deployment {
		d := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/app@sha256:" + strings.Repeat("a", 64)}
		if candidate {
			d.RolloutState = "rolling_out"
			if probe {
				d.OverrideReadinessProbe = json.RawMessage(`{"path":"/ready"}`)
			}
		}
		out, err := s.CreateDeployment(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, out.ID); err != nil {
			t.Fatal(err)
		}
		out, err = s.DeploymentByID(ctx, out.ID)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	old := create(false)
	next := create(true)
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	vm := &rolloutRecoveryVM{}
	e, err := sched.NewEngine(ctx, s, sched.NewNodeLedger(), vm, rolloutRecoveryNotifier{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	*e.ServiceReconcileSubmit() = func(context.Context, string) {}
	return rolloutRecoveryFixture{store: s, app: app, node: node, old: old, next: next, vm: vm, engine: e}
}
func (f rolloutRecoveryFixture) running(t *testing.T, dep state.Deployment) state.Instance {
	t.Helper()
	i, e := f.store.CreateInstanceWithMode(t.Context(), f.app.ID, dep.ID, "running", 128, f.node.ID, uuid.NewString(), "service")
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func (f rolloutRecoveryFixture) observe(t *testing.T, i state.Instance, status string) {
	t.Helper()
	f.observeContext(t.Context(), t, i, status)
}

func (f rolloutRecoveryFixture) observeContext(ctx context.Context, t *testing.T, i state.Instance, status string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"app_id": f.app.ID, "instance_id": i.ID, "node_id": i.NodeID, "wake_id": i.WakeID, "status": status})
	err := f.store.(interface {
		AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
	}).AppendEventAt(ctx, "vmmd", "wake.app_readiness", nil, body, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
}

// Fixtures insert existing replicas directly; production seeds these reservations
// before it accepts reconciliation or wake work.
func rolloutRecoveryReconcile(t *testing.T, e *sched.Engine, deploymentID string) {
	t.Helper()
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.ReconcileServiceDeployment(t.Context(), deploymentID)
}

type rolloutRecoveryWeights struct{ s state.Store }

func (w rolloutRecoveryWeights) LiveDeployments(ctx context.Context, id string) ([]gateway.DeploymentWeightsRow, error) {
	ds, e := w.s.LiveDeployments(ctx, id)
	var out []gateway.DeploymentWeightsRow
	for _, d := range ds {
		out = append(out, gateway.DeploymentWeightsRow{ID: d.ID, TrafficPercent: d.TrafficPercent})
	}
	return out, e
}
func (f rolloutRecoveryFixture) picker(t *testing.T, old, next state.Instance, ready bool) *gateway.PGBackend {
	t.Helper()
	b := gateway.NewPGBackend(nil, nil, nil).WithStore(rolloutRecoveryWeights{f.store})
	b.RecordTarget(f.app.ID, gateway.Target{AppID: f.app.ID, InstanceID: old.ID, DeploymentID: f.old.ID, NodeID: old.NodeID, WakeID: old.WakeID})
	b.RecordTarget(f.app.ID, gateway.Target{AppID: f.app.ID, InstanceID: next.ID, DeploymentID: f.next.ID, NodeID: next.NodeID, WakeID: next.WakeID, RequiresReadiness: true, ReadinessGates: &gateway.ReadinessGates{RequiredSources: []string{"primary_app"}}})
	status := "ready"
	if !ready {
		status = "unready"
	}
	b.SetInstanceReadinessForTarget(f.app.ID, next.ID, next.WakeID, next.NodeID, "primary_app", status, time.Now().UTC(), 1)
	if e := b.RefreshDeploymentWeights(t.Context(), f.app.ID); e != nil {
		t.Fatal(e)
	}
	return b
}

func TestServiceReadinessWithdrawalRestoresTraffic(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, cutover := range []bool{false, true} {
				t.Run(fmt.Sprint(cutover), func(t *testing.T) {
					f := newRolloutRecoveryFixture(t, s, 1, true)
					old := f.running(t, f.old)
					next := f.running(t, f.next)
					f.observe(t, next, "ready")
					if cutover {
						if _, e := s.BeginServiceRolloutCutover(t.Context(), f.next.ID); e != nil {
							t.Fatal(e)
						}
					}
					before := f.picker(t, old, next, true).Pick(f.app.ID)
					if !before.OK {
						t.Fatal("positive control has no routable target")
					}
					f.observe(t, next, "unready")
					rolloutRecoveryReconcile(t, f.engine, f.next.ID)
					candidate, _ := s.DeploymentByID(t.Context(), f.next.ID)
					previous, _ := s.DeploymentByID(t.Context(), f.old.ID)
					pick := f.picker(t, old, next, false).Pick(f.app.ID)
					t.Logf("cutover=%v candidate=%s/%s/%d previous=%s/%d pick.ok=%v pick.deployment=%s cold=%s", cutover, candidate.Status, candidate.RolloutState, candidate.TrafficPercent, previous.Status, previous.TrafficPercent, pick.OK, pick.Target.DeploymentID, pick.ColdBucket)
					if !pick.OK || pick.Target.DeploymentID != f.old.ID {
						t.Error("healthy predecessor is not routable after candidate readiness withdrawal")
					}
				})
			}
		})
	}
}

func TestServicePartialRolloutPreservesServingCapacity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprint(partial), func(t *testing.T) {
					f := newRolloutRecoveryFixture(t, s, 2, false)
					f.running(t, f.old)
					f.running(t, f.old)
					if partial {
						f.running(t, f.next)
					}
					rolloutRecoveryReconcile(t, f.engine, f.next.ID)
					rows, e := s.ListInstancesForApp(t.Context(), f.app.ID)
					if e != nil {
						t.Fatal(e)
					}
					oldRunning, nextRunning := 0, 0
					for _, i := range rows {
						if i.State == "running" {
							if i.DeploymentID == f.old.ID {
								oldRunning++
							}
							if i.DeploymentID == f.next.ID {
								nextRunning++
							}
						}
					}
					previous, _ := s.DeploymentByID(t.Context(), f.old.ID)
					candidate, _ := s.DeploymentByID(t.Context(), f.next.ID)
					boots, parks := f.vm.counts()
					t.Logf("partial=%v desired=2 old.running=%d old.traffic=%d candidate.running=%d candidate.traffic=%d boots=%d parks=%d", partial, oldRunning, previous.TrafficPercent, nextRunning, candidate.TrafficPercent, boots, parks)
					if oldRunning < 2 && candidate.TrafficPercent == 0 {
						t.Error("serving capacity retired while replacement generation still has zero traffic")
					}
				})
			}
		})
	}
}

func TestServiceCohortProgressesWithinConcurrencyGrant(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			f := newRolloutRecoveryFixture(t, s, 2, false)
			limit := 2
			if _, err := s.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{MaxConcurrency: &limit}); err != nil {
				t.Fatal(err)
			}
			f.running(t, f.old)
			f.running(t, f.old)
			f.running(t, f.next)
			rolloutRecoveryReconcile(t, f.engine, f.next.ID)
			rows, err := s.ListInstancesForApp(t.Context(), f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldRunning, live := 0, 0
			for _, i := range rows {
				if state.State(i.State).CountsForConcurrency() {
					live++
				}
				if i.DeploymentID == f.old.ID && i.State == "running" {
					oldRunning++
				}
			}
			boots, parks := f.vm.counts()
			if oldRunning != 1 || live > limit+api.RolloutConcurrencyGrant || parks != 0 || boots != 0 {
				t.Fatalf("bounded rollout: old=%d live=%d boots=%d parks=%d", oldRunning, live, boots, parks)
			}
			dep, err := s.DeploymentByID(t.Context(), f.next.ID)
			if err != nil || dep.TrafficPercent != 50 || dep.RolloutState != "rolling_out" {
				t.Fatalf("bounded traffic step: %+v %v", dep, err)
			}
		})
	}
}

type rolloutWithdrawalNotifier struct {
	hook func(context.Context, string, string)
}

func (n rolloutWithdrawalNotifier) Notify(ctx context.Context, channel, payload string) error {
	n.hook(ctx, channel, payload)
	return nil
}

func TestServiceReadinessWithdrawalDuringFinalization(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			f := newRolloutRecoveryFixture(t, s, 1, true)
			old := f.running(t, f.old)
			next := f.running(t, f.next)
			f.observe(t, next, "ready")
			withdrew := false
			n := rolloutWithdrawalNotifier{hook: func(ctx context.Context, _ string, _ string) {
				current, err := s.DeploymentByID(ctx, f.next.ID)
				if err == nil && !withdrew && current.TrafficPercent == 100 {
					withdrew = true
					f.observeContext(ctx, t, next, "unready")
				}
			}}
			e, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), f.vm, n, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			*e.ServiceReconcileSubmit() = func(context.Context, string) {}
			rolloutRecoveryReconcile(t, e, f.next.ID)
			got, err := s.DeploymentByID(t.Context(), f.next.ID)
			if err != nil {
				t.Fatal(err)
			}
			prev, err := s.DeploymentByID(t.Context(), f.old.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !withdrew || got.RolloutState != "aborted" || got.TrafficPercent != 0 || prev.TrafficPercent != 100 {
				t.Fatalf("withdrawal recovery: withdrew=%v candidate=%+v predecessor=%+v", withdrew, got, prev)
			}
			if pick := f.picker(t, old, next, false).Pick(f.app.ID); !pick.OK || pick.Target.DeploymentID != f.old.ID {
				t.Fatalf("healthy predecessor is not routable: %+v", pick)
			}
		})
	}
}

func TestServiceReadinessWithdrawalKeepsBindingFence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			f := newRolloutRecoveryFixture(t, s, 1, true)
			f.running(t, f.old)
			next := f.running(t, f.next)
			f.observe(t, next, "ready")
			if _, err := s.BeginServiceRolloutCutover(t.Context(), f.next.ID); err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			if _, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(t.Context(), f.app.AccountID, f.app.ID, f.next.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
				t.Fatal(err)
			}
			f.observe(t, next, "unready")
			rolloutRecoveryReconcile(t, f.engine, f.next.ID)
			got, err := s.DeploymentByID(t.Context(), f.next.ID)
			if err != nil {
				t.Fatal(err)
			}
			gate := got.ServiceRolloutHandoff.BindingsCheck
			if !got.ServiceRolloutHandoff.ActiveAbort() || gate == nil || gate.Action != state.ServiceRolloutActionAbort || gate.DeploymentID != f.old.ID || gate.Status != "pending" {
				t.Fatalf("abort did not retain a fresh binding request: %+v", got.ServiceRolloutHandoff)
			}
			if got.TrafficPercent != 100 {
				t.Fatal("missing binding fence allowed a traffic change")
			}
			// A replacement scheduler resumes the abort instead of promoting again.
			f.observe(t, next, "ready")
			restarted, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), f.vm, rolloutRecoveryNotifier{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			*restarted.ServiceReconcileSubmit() = func(context.Context, string) {}
			rolloutRecoveryReconcile(t, restarted, f.next.ID)
			again, err := s.DeploymentByID(t.Context(), f.next.ID)
			if err != nil || !again.ServiceRolloutHandoff.ActiveAbort() || again.ServiceRolloutHandoff.BindingsCheck.RequestID != gate.RequestID {
				t.Fatalf("restart replaced abort intent: %+v %v", again.ServiceRolloutHandoff, err)
			}
		})
	}
}

func TestServiceCohortCompletesWithHeadroom(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			f := newRolloutRecoveryFixture(t, s, 2, false)
			f.running(t, f.old)
			f.running(t, f.old)
			f.running(t, f.next)
			e, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), &sched.FakeVMM{}, rolloutRecoveryNotifier{}, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			*e.ServiceReconcileSubmit() = func(context.Context, string) {}
			rolloutRecoveryReconcile(t, e, f.next.ID)
			dep, err := s.DeploymentByID(t.Context(), f.next.ID)
			if err != nil {
				t.Fatal(err)
			}
			if dep.RolloutState != "complete" || dep.TrafficPercent != 100 {
				t.Fatalf("candidate cohort did not complete: %+v", dep)
			}
			rows, err := s.ListInstancesForApp(t.Context(), f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldRunning, newRunning := 0, 0
			for _, i := range rows {
				if i.State == "running" {
					if i.DeploymentID == f.old.ID {
						oldRunning++
					}
					if i.DeploymentID == f.next.ID {
						newRunning++
					}
				}
			}
			if oldRunning != 0 || newRunning != 2 {
				t.Fatalf("completed cohort: old=%d new=%d", oldRunning, newRunning)
			}
		})
	}
}

type rolloutRouteNotifier struct {
	events chan db.Notification
	route  func(context.Context, db.DeploymentRouteChangedPayload)
}

func (n *rolloutRouteNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, error) {
	return n.events, nil
}
func (n *rolloutRouteNotifier) Notify(ctx context.Context, channel, payload string) error {
	if channel == db.NotifyDeploymentRouteChanged {
		var p db.DeploymentRouteChangedPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return err
		}
		n.route(ctx, p)
	}
	return nil
}

func TestServiceReadinessWithdrawalInterruptsFleetHandoff(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, phase := range []string{"routing", "draining"} {
				t.Run(phase, func(t *testing.T) {
					f := newRolloutRecoveryFixture(t, s, 1, true)
					f.running(t, f.old)
					next := f.running(t, f.next)
					f.observe(t, next, "ready")
					role, url := "compute-only", "tcp://gateway.internal:8080"
					node, err := s.UpsertComputeNode(t.Context(), state.ComputeNode{Name: "withdrawal-gateway", Role: &role, GatewayTargetURL: &url, TargetURL: "unix:///tmp/withdrawal-vmmd.sock", VPCPUs: 4, VCPUBudget: 16, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4096, Active: true, Lifecycle: state.NodeLifecycleActive})
					if err != nil {
						t.Fatal(err)
					}
					n := &rolloutRouteNotifier{events: make(chan db.Notification, 4)}
					ack := func(p db.DeploymentRouteChangedPayload) {
						body, _ := json.Marshal(db.DeploymentRouteAckPayload{Node: node.Name, Generation: p.Generation})
						n.events <- db.Notification{Channel: db.NotifyDeploymentRouteAck, Payload: string(body)}
					}
					waitPhase := func(ctx context.Context, action string) error {
						ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
						defer cancel()
						ticker := time.NewTicker(5 * time.Millisecond)
						defer ticker.Stop()
						for {
							d, err := s.DeploymentByID(ctx, f.next.ID)
							if err != nil {
								return err
							}
							if d.ServiceRolloutHandoff.Action == action && d.ServiceRolloutHandoff.Phase == state.ServiceRolloutPhaseDraining {
								return nil
							}
							select {
							case <-ctx.Done():
								return ctx.Err()
							case <-ticker.C:
							}
						}
					}
					async := make(chan error, 1)
					promote, abort := 0, 0
					n.route = func(ctx context.Context, p db.DeploymentRouteChangedPayload) {
						if p.DeploymentID == f.next.ID {
							promote++
							if promote != 1 {
								return
							}
							if phase == "routing" {
								// No ACK is emitted for the now-unavailable candidate. The wait must
								// observe readiness rather than consume its full timeout.
								f.observeContext(ctx, t, next, "unready")
								if err := s.UpdateInstanceState(ctx, next.ID, "stopped"); err != nil {
									t.Error(err)
								}
							} else {
								ack(p)
								go func() {
									// Observe the reverse handoff after the forward notification
									// returns, retaining its values under a bounded lifetime.
									asyncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
									defer cancel()
									if err := waitPhase(asyncCtx, state.ServiceRolloutActionPromote); err != nil {
										async <- err
										return
									}
									body, _ := json.Marshal(map[string]string{"app_id": f.app.ID, "instance_id": next.ID, "wake_id": next.WakeID, "node_id": next.NodeID, "status": "unready"})
									err := s.(interface {
										AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
									}).AppendEventAt(asyncCtx, "vmmd", "wake.app_readiness", nil, body, time.Now().UTC())
									if err == nil {
										err = waitPhase(asyncCtx, state.ServiceRolloutActionAbort)
									}
									// The failing process exits only after the reverse route is ACKed.
									if err == nil {
										err = s.UpdateInstanceState(asyncCtx, next.ID, "stopped")
									}
									async <- err
								}()
							}
						} else if p.DeploymentID == f.old.ID {
							abort++
							ack(p)
						}
					}
					e, err := sched.NewEngine(t.Context(), s, sched.NewNodeLedger(), f.vm, n, "1.10.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
					if err != nil {
						t.Fatal(err)
					}
					*e.ServiceReconcileSubmit() = func(context.Context, string) {}
					rolloutRecoveryReconcile(t, e, f.next.ID)
					if phase == "draining" {
						if err := <-async; err != nil {
							t.Fatal(err)
						}
					}
					got, err := s.DeploymentByID(t.Context(), f.next.ID)
					if err != nil {
						t.Fatal(err)
					}
					previous, err := s.DeploymentByID(t.Context(), f.old.ID)
					if err != nil {
						t.Fatal(err)
					}
					if promote == 0 || abort == 0 || got.RolloutState != "aborted" || got.ServiceRolloutHandoff.Phase != state.ServiceRolloutPhaseComplete || got.ServiceRolloutHandoff.AcknowledgedAt == nil || previous.TrafficPercent != 100 {
						t.Fatalf("fleet recovery: promote=%d abort=%d candidate=%+v predecessor=%+v", promote, abort, got, previous)
					}
				})
			}
		})
	}
}
