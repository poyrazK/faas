package sched

// adr: 028 — scheduler ownership and per-app admission across nodes.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeRepairsTerminalPeerLedgerEntry(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 1)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	first, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("first Wake: %v", err)
	}
	if got := e.Ledger().Concurrency(app.ID); got != 1 {
		t.Fatalf("first wake concurrency = %d, want 1", got)
	}

	// Model a peer scheduler winning the park transition. The durable row is
	// terminal, while this process still holds the admission reservation.
	if err := store.UpdateInstanceState(context.Background(), first.InstanceID, string(state.StateParked)); err != nil {
		t.Fatalf("peer park transition: %v", err)
	}

	second, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("second Wake after peer park: %v", err)
	}
	if second.InstanceID == "" || second.InstanceID == first.InstanceID {
		t.Fatalf("second wake instance = %q, first = %q", second.InstanceID, first.InstanceID)
	}
	if got := e.Ledger().Concurrency(app.ID); got != 1 {
		t.Fatalf("reconciled concurrency = %d, want 1", got)
	}
}

func TestParkAppOwnerlessSchedulerSkipsRemoteShard(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 1)
	if err := store.SetAppNodeID(context.Background(), app.ID, "remote-compute-node"); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	acted, err := e.ParkApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ParkApp: %v", err)
	}
	if acted != 0 {
		t.Fatalf("ParkApp acted = %d, want 0", acted)
	}
	if vmm.snapshots != 0 || vmm.destroys != 0 {
		t.Fatalf("remote shard touched: snapshots=%d destroys=%d", vmm.snapshots, vmm.destroys)
	}
}
