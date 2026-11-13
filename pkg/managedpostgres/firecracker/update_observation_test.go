package firecracker

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestObserveUpdatePreservesPendingResizeAndDoesNotMutate(t *testing.T) {
	p, scheduler, _ := newTestProvider(t)
	spec := devSpec()
	if _, err := p.Provision(t.Context(), managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	request := managedpostgres.UpdateRequest{ResourceID: testDatabaseID, PreviousSpec: spec, Spec: spec, Generation: 2}
	request.Spec.Class = managedpostgres.ClassBurstable
	for _, pending := range []bool{false, true} {
		scheduler.mu.Lock()
		scheduler.vms[testDatabaseID].ResizePending = pending
		scheduler.mu.Unlock()
		observed, err := p.ObserveUpdate(t.Context(), request)
		want := managedpostgres.ProviderStatusReady
		if pending {
			want = managedpostgres.ProviderStatusPending
		}
		if err != nil || observed.Status != want || observed.Spec != spec || len(scheduler.resizes) != 0 {
			t.Fatalf("update observation changed the runtime or hid a resize: observed=%+v resizes=%d err=%v", observed, len(scheduler.resizes), err)
		}
	}
}
