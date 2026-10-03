package sched

// spec: §6.1 — stale watchdog work must preserve resident accounting (§6.2).

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type watchdogReadErrorStore struct {
	*state.MemStore
	readErr error
}

func (s *watchdogReadErrorStore) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	if s.readErr != nil {
		return state.Instance{}, s.readErr
	}
	return s.MemStore.InstanceByID(ctx, id)
}

func seedWatchdogReservation(t *testing.T, store state.Store, status state.State) (*Engine, *fakeVMM, state.Instance) {
	t.Helper()
	ctx := context.Background()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(status), app.RAMMB, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := e.ledger.Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID,
		Plan: api.PlanPro, RAMMB: app.RAMMB, VCPU: 2, CPUMillicores: 1000,
		NodeID: ins.NodeID, MaxConcurrency: app.MaxConcurrency}); err != nil {
		t.Fatal(err)
	}
	return e, vmm, ins
}

func assertWatchdogReservationIntact(t *testing.T, e *Engine, vmm *fakeVMM, ins state.Instance) {
	t.Helper()
	if !e.ledger.ResidentFor(ins.ID) || e.ledger.ResidentRAMForNode(ins.NodeID) != api.BillableRAMMBWithSidecars(ins.RAMMB, nil) ||
		e.ledger.UsedVCPUForNode(ins.NodeID) != 2 || e.ledger.UsedCPUMillicoresForNode(ins.NodeID) != 1000 ||
		e.ledger.Concurrency(ins.AppID) != 1 {
		t.Fatal("watchdog released a live reservation")
	}
	if vmm.destroys != 0 {
		t.Fatal("stale watchdog destroyed a VM")
	}
}

func TestKillStuckPreservesReservationOnReadFailure(t *testing.T) {
	for _, readErr := range []error{errors.New("database unavailable"), context.Canceled, context.DeadlineExceeded} {
		t.Run(readErr.Error(), func(t *testing.T) {
			store := &watchdogReadErrorStore{MemStore: state.NewMemStore()}
			e, vmm, ins := seedWatchdogReservation(t, store, state.StateWaking)
			store.readErr = readErr
			err := e.KillStuck(context.Background(), ins.ID, ins.AppID, StuckWakingTimeout)
			if !errors.Is(err, readErr) {
				t.Errorf("KillStuck error=%v, want wrapped %v", err, readErr)
			}
			assertWatchdogReservationIntact(t, e, vmm, ins)
		})
	}
}

func TestKillStuckPreservesChangedResidentReservation(t *testing.T) {
	for _, status := range []state.State{state.StateWaking, state.StateColdBooting, state.StateRunning, state.StateDraining,
		state.StateSnapshotting, state.StateMigrating, state.StateWarm, state.StateEvictingAccountDeleting} {
		for _, reason := range []StuckReason{StuckWakingTimeout, StuckColdBootTimeout, StuckSnapshotTimeout} {
			if status == expectedStateForReason(reason) {
				continue
			}
			t.Run(fmt.Sprintf("%s/%s", status, reason), func(t *testing.T) {
				store := state.NewMemStore()
				e, vmm, ins := seedWatchdogReservation(t, store, status)
				if err := e.KillStuck(context.Background(), ins.ID, ins.AppID, reason); err != nil {
					t.Fatal(err)
				}
				assertWatchdogReservationIntact(t, e, vmm, ins)
				fresh, err := store.InstanceByID(context.Background(), ins.ID)
				if err != nil || fresh.State != string(status) {
					t.Fatalf("row changed: %+v %v", fresh, err)
				}
			})
		}
	}
}

func TestKillStuckReleasesOnlyConfirmedTerminalMismatch(t *testing.T) {
	for _, status := range []state.State{state.StateParked, state.StateStopped, state.StateFailed} {
		t.Run(string(status), func(t *testing.T) {
			store := state.NewMemStore()
			e, vmm, ins := seedWatchdogReservation(t, store, status)
			if err := e.KillStuck(context.Background(), ins.ID, ins.AppID, StuckWakingTimeout); err != nil {
				t.Fatal(err)
			}
			if e.ledger.ResidentFor(ins.ID) {
				t.Fatal("terminal reservation not released")
			}
			if vmm.destroys != 0 {
				t.Fatal("terminal mismatch destroyed a VM")
			}
		})
	}
}

func TestKillStuckAlreadyRemovedRowRemainsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	e, vmm, ins := seedWatchdogReservation(t, store, state.StateWaking)
	if err := store.DeleteInstance(context.Background(), ins.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.KillStuck(context.Background(), ins.ID, ins.AppID, StuckWakingTimeout); err != nil {
		t.Fatal(err)
	}
	if e.ledger.ResidentFor(ins.ID) || vmm.destroys != 0 {
		t.Fatal("missing-row cleanup changed")
	}
}
