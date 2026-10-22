// adr: 1151 — parking an app parks its frozen instances too.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production-us 2026-10-22 (h12-fnrs): `gregale park` on a function whose
// instance was frozen timed out after 60 s. ParkApp had no FROZEN case, so it
// left the VM frozen and resident and reported nothing to park.
func TestParkAppParksFrozenInstances(t *testing.T) {
	f := sched.NewFrozenFixture(t, true)
	e, store, ins := *f.E(), *f.Store(), *f.Ins()
	e.EnableFrozenTier()
	ctx := t.Context()
	if handled, err := e.FreezeIdle(ctx, ins.ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle = %v, %v; want frozen", handled, err)
	}
	destroys := *(*f.Vmm()).Destroys()

	acted, err := e.ParkApp(ctx, f.App().ID)
	if err != nil {
		t.Fatalf("ParkApp: %v", err)
	}
	row, err := store.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acted != 1 || state.State(row.State) != state.StateParked {
		t.Fatalf("ParkApp acted on %d instances, frozen instance now %s; want 1 and parked", acted, row.State)
	}
	if *(*f.Vmm()).Destroys() != destroys+1 || e.Ledger().ResidentFor(ins.ID) || e.Ledger().IsFrozen(ins.ID) {
		t.Fatalf("destroys %d→%d, resident %v, frozen %v; want the VM destroyed and its booking released",
			destroys, *(*f.Vmm()).Destroys(), e.Ledger().ResidentFor(ins.ID), e.Ledger().IsFrozen(ins.ID))
	}
}
