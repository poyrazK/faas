package sched_test

// adr: 199

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// An app at max_concurrency 2 with two serving instances restarts: the
// replacement takes the one-slot rollout grant.
func TestRefreshRuntimeConfigAtMaxConcurrencyUsesTheGrant(t *testing.T) {
	store := state.NewMemStore()
	_, app, serving := sched.SeedApp(t, store, api.PlanScale, 256, 2)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	var old []string
	for range 2 {
		res, err := engine.AdmitAndDispatchWithOptions(t.Context(), app.ID, serving.ID, "normal", sched.TriggerAppWake, true, true)
		if err != nil || res.InstanceID == "" {
			t.Fatalf("scale-out admission: %+v, %v", res, err)
		}
		old = append(old, res.InstanceID)
	}
	if _, err := state.InvalidateAppSnapshots(t.Context(), store, app.ID); err != nil {
		t.Fatal(err)
	}
	out, err := engine.RefreshRuntimeConfig(t.Context(), app.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("RefreshRuntimeConfig at max_concurrency: %v", err)
	}
	if out.Instance == nil || out.Instance.DeploymentID != serving.ID {
		t.Fatalf("refreshed instance = %+v; want a fresh serving instance", out.Instance)
	}
	assertRetired(t, store, old)
}

// production 2026-11-15 (managed PostgreSQL journey): an app at
// max_concurrency 1 had one serving instance, but its scheduler's ledger
// counted a second slot that no instance row held, so the stale-admission
// repair could not release it. Every replacement was refused with
// "max_concurrency is 2; 2 already live" and the restart, and the credential
// rotation waiting on it, stalled until the serving instance idled out. The
// restart now retires its predecessor and boots the replacement.
func TestRefreshRuntimeConfigRetiresThePredecessorWhenALeakedSlotFillsTheGrant(t *testing.T) {
	store := state.NewMemStore()
	_, app, serving := sched.SeedApp(t, store, api.PlanScale, 256, 1)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	res, err := engine.AdmitAndDispatchWithOptions(t.Context(), app.ID, serving.ID, "normal", sched.TriggerAppWake, true, true)
	if err != nil || res.InstanceID == "" {
		t.Fatalf("wake: %+v, %v", res, err)
	}
	running, err := store.InstanceByID(t.Context(), res.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	// A reservation with no instance row: nothing the repair can judge.
	if err := (*engine.XLedger()).Admit(sched.Request{Instance: uuid.NewString(), AppID: app.ID, DeploymentID: serving.ID,
		DeploymentScope: serving.Scope, Plan: api.PlanScale, RAMMB: 256, MaxConcurrency: 1, NodeID: running.NodeID, AllowConcurrencyOverlap: true}); err != nil {
		t.Fatalf("seed the leaked reservation: %v", err)
	}
	if _, err := state.InvalidateAppSnapshots(t.Context(), store, app.ID); err != nil {
		t.Fatal(err)
	}
	out, err := engine.RefreshRuntimeConfig(t.Context(), app.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("RefreshRuntimeConfig with a leaked slot: %v", err)
	}
	if out.Instance == nil || out.Instance.DeploymentID != serving.ID || out.Instance.InstanceID == res.InstanceID {
		t.Fatalf("refreshed instance = %+v; want a fresh serving instance", out.Instance)
	}
	assertRetired(t, store, []string{res.InstanceID})
}

func assertRetired(t *testing.T, store *state.MemStore, ids []string) {
	t.Helper()
	for _, id := range ids {
		instance, err := store.InstanceByID(t.Context(), id)
		if err != nil || sched.RuntimeConfigResident(instance) {
			t.Fatalf("predecessor %s = %+v, %v; want retired", id, instance, err)
		}
	}
}
