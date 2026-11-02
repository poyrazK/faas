// adr: 20261101091815 — a park that discards a VM keeps its reservation
// until the VM is really gone.
package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// After a RAM change a park destroys the VM instead of capturing it. It used
// to release the reservation and stop the row even when the destroy failed,
// so the ledger forgot memory a live VM still held.
func TestParkDiscardKeepsTheReservationWhenDestroyFails(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, 5)
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	woken, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	ram := 256
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
	*vmm.DestroyErr() = errors.New("fake: firecracker wedged")
	if err := e.Park(ctx, woken.InstanceID); err == nil {
		t.Fatal("Park succeeded although the destroy failed")
	}
	if !e.Ledger().ResidentFor(woken.InstanceID) {
		t.Fatal("a failed destroy released the reservation")
	}
	if row, err := store.InstanceByID(ctx, woken.InstanceID); err != nil || row.State != string(state.StateRunning) {
		t.Fatalf("row after a failed destroy: %q, %v; want running", row.State, err)
	}
	*vmm.DestroyErr() = nil
	if err := e.Park(ctx, woken.InstanceID); err != nil {
		t.Fatalf("Park after the destroy recovered: %v", err)
	}
	if e.Ledger().ResidentFor(woken.InstanceID) {
		t.Fatal("the discarded VM kept its reservation")
	}
	if row, err := store.InstanceByID(ctx, woken.InstanceID); err != nil || row.State != string(state.StateStopped) {
		t.Fatalf("row after the discard: %q, %v; want stopped", row.State, err)
	}
}
