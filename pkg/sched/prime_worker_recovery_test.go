package sched_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func primeWorkerRecoveryLoop(e *sched.Engine) *sched.Loop {
	return sched.NewLoop(nil, e, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPrimeRecoveryReplacesStaleRunningWorker(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := rolloutRecoveryStore(t, backend)
			for _, change := range []string{"fresh-running", "stale-running", "stale-stopped", "booting", "missing-proof", "cleanup-retry"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, d := primeAttemptWorker(t, s)
					if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "MODE", "old"); err != nil {
						t.Fatal(err)
					}
					v := &sched.FakeVMM{}
					var old state.Instance
					if change == "booting" || change == "missing-proof" {
						node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
						if err != nil {
							t.Fatal(err)
						}
						mode := "cold_booting"
						if change == "missing-proof" {
							mode = "running"
						}
						old, err = s.CreateInstanceWithMode(ctx, app.ID, d.ID, mode, app.RAMMB, node.ID, uuid.NewString(), "worker")
						if err != nil {
							t.Fatal(err)
						}
					} else {
						initial := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
						if err := initial.Prime(ctx, app.ID, d.ID); err != nil {
							t.Fatal(err)
						}
						rows, err := s.ListInstancesForApp(ctx, app.ID)
						if err != nil || len(rows) != 1 {
							t.Fatalf("initial worker: %+v %v", rows, err)
						}
						old = rows[0]
					}
					if change == "stale-running" || change == "stale-stopped" || change == "cleanup-retry" {
						if err := s.UpsertAppEnv(ctx, app.AccountID, app.ID, "MODE", "new"); err != nil {
							t.Fatal(err)
						}
					}
					if change == "stale-stopped" {
						if err := s.UpdateInstanceState(ctx, old.ID, "stopped"); err != nil {
							t.Fatal(err)
						}
					}
					e := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
					if err := e.SeedLedger(ctx); err != nil {
						t.Fatal(err)
					}
					loop := primeWorkerRecoveryLoop(e)
					initialBoots, initialDestroys := *v.ColdBoots(), *v.Destroys()
					if change == "cleanup-retry" {
						*v.DestroyErr() = errors.New("temporary teardown failure")
						if err := loop.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err == nil {
							t.Fatal("teardown failure was hidden")
						}
						got, err := s.InstanceByID(ctx, old.ID)
						if err != nil || got.State != "draining" || *v.ColdBoots() != initialBoots || e.Ledger().ResidentRAM() == 0 {
							t.Fatalf("failed teardown lost ownership/accounting: state=%s boots=%d RAM=%d err=%v", got.State, *v.ColdBoots(), e.Ledger().ResidentRAM(), err)
						}
						*v.DestroyErr() = nil
					}
					for range 3 {
						if err := loop.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err != nil {
							t.Fatal(err)
						}
						loop.WaitPrimes()
					}
					wantBoots, wantDestroys := 1, 1
					if change == "fresh-running" || change == "booting" {
						wantBoots, wantDestroys = 0, 0
					}
					if change == "stale-stopped" {
						wantDestroys = 0
					}
					if *v.ColdBoots()-initialBoots != wantBoots || *v.Destroys()-initialDestroys != wantDestroys {
						t.Fatalf("recovery effects: boots=%d destroys=%d want=%d/%d", *v.ColdBoots()-initialBoots, *v.Destroys()-initialDestroys, wantBoots, wantDestroys)
					}
					if wantBoots > 0 {
						rows, err := s.ListInstancesForApp(ctx, app.ID)
						if err != nil {
							t.Fatal(err)
						}
						var replacement state.Instance
						for _, i := range rows {
							if i.ID != old.ID && i.State == "running" {
								replacement = i
							}
						}
						if replacement.ID == "" {
							t.Fatal("fresh replacement was not published")
						}
						values, err := s.(state.RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, d.ID)
						if err != nil {
							t.Fatal(err)
						}
						current, err := state.NewRuntimeAppConfigFence(values)
						if err != nil {
							t.Fatal(err)
						}
						proof, err := s.(state.RuntimeInstancePublicationStore).InstanceRuntimeConfigFence(ctx, app.AccountID, app.ID, replacement.ID)
						if err != nil || proof != current {
							t.Fatalf("replacement did not acknowledge current inputs: %v", err)
						}
					}
				})
			}
		})
	}
}

type failingPrimeRecoveryInputs struct{ MemStore *state.MemStore }

func (s *failingPrimeRecoveryInputs) RuntimeAppValuesForDeployment(context.Context, string, string, string) (state.RuntimeAppValuesSnapshot, error) {
	return state.RuntimeAppValuesSnapshot{}, errors.New("runtime inputs unavailable")
}

func TestPrimeRecoveryInputFailurePreservesRunningWorker(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	app, d := primeAttemptWorker(t, mem)
	v := &sched.FakeVMM{}
	initial := sched.XNewEngine(t, mem, v, &sched.FakeNotifier{}, "1.10.0")
	if err := initial.Prime(ctx, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := mem.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("initial worker: %+v %v", rows, err)
	}
	s := statetest.NewMem(&failingPrimeRecoveryInputs{MemStore: mem})
	e := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := primeWorkerRecoveryLoop(e).RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("input read failure was hidden")
	}
	got, err := mem.InstanceByID(ctx, rows[0].ID)
	if err != nil || got.State != "running" || *v.ColdBoots() != 1 || *v.Destroys() != 0 {
		t.Fatalf("read failure caused VM effects: state=%s boots=%d destroys=%d err=%v", got.State, *v.ColdBoots(), *v.Destroys(), err)
	}
}

type completedPrimeRetirement struct {
	MemStore           *state.MemStore
	deleted, completed bool
}

func (s *completedPrimeRetirement) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	i, err := s.MemStore.InstanceByID(ctx, id)
	if err != nil || i.State != "draining" || s.completed {
		return i, err
	}
	// Model a peer finishing the claimed teardown before this engine reloads.
	s.completed = true
	if err := s.MemStore.UpdateInstanceStateToTerminal(ctx, id, "stopped", time.Now().UTC()); err != nil {
		return state.Instance{}, err
	}
	if s.deleted {
		if err := s.MemStore.DeleteInstance(ctx, id); err != nil {
			return state.Instance{}, err
		}
	}
	return s.MemStore.InstanceByID(ctx, id)
}

func TestPrimeRecoveryCompletedTeardownReleasesLocalReservation(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "deleted"}[deleted], func(t *testing.T) {
			ctx := t.Context()
			mem := state.NewMemStore()
			app, d := primeAttemptWorker(t, mem)
			one := 1
			var err error
			app, err = mem.UpdateApp(ctx, app.ID, state.UpdateAppParams{MaxConcurrency: &one})
			if err != nil {
				t.Fatal(err)
			}
			v := &sched.FakeVMM{}
			initial := sched.XNewEngine(t, mem, v, &sched.FakeNotifier{}, "1.10.0")
			if err := initial.Prime(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			rows, err := mem.ListInstancesForApp(ctx, app.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("initial worker: %+v %v", rows, err)
			}
			oldID := rows[0].ID
			if err := mem.UpsertAppEnv(ctx, app.AccountID, app.ID, "MODE", "new"); err != nil {
				t.Fatal(err)
			}
			s := statetest.NewMem(&completedPrimeRetirement{MemStore: mem, deleted: deleted})
			e := sched.XNewEngine(t, s, v, &sched.FakeNotifier{}, "1.10.0")
			if err := e.SeedLedger(ctx); err != nil {
				t.Fatal(err)
			}
			loop := primeWorkerRecoveryLoop(e)
			if err := loop.RecoverPrimeCandidate(ctx, d.ID, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			loop.WaitPrimes()
			ids := e.Ledger().InstancesForApp(app.ID)
			if *v.ColdBoots() != 2 || *v.Destroys() != 0 || len(ids) != 1 || ids[0] == oldID || e.Ledger().ResidentRAM() != app.RAMMB+api.PerVMOverheadMB {
				t.Fatalf("completed teardown blocked or duplicated replacement: boots=%d destroys=%d reservations=%v RAM=%d", *v.ColdBoots(), *v.Destroys(), ids, e.Ledger().ResidentRAM())
			}
			replacement, err := mem.InstanceByID(ctx, ids[0])
			if err != nil || replacement.State != "running" || replacement.DeploymentID != d.ID {
				t.Fatalf("replacement was not published: %+v %v", replacement, err)
			}
		})
	}
}
