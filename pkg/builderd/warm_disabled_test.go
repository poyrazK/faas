// adr: 003 — builds cold-boot a fresh builder VM unless warm builders are opted in.

package builderd

import (
	"context"
	"testing"
	"time"
)

// TestPrepareWarmBuilderDisabledColdBoots pins the production default: with a
// matching retained snapshot available, a disabled builderd neither restores
// nor keeps the next builder warm. Restoring that snapshot failed production
// builds because the guest resumed with pre-edit filesystem state.
func TestPrepareWarmBuilderDisabledColdBoots(t *testing.T) {
	vm := &warmBuilderTestVM{}
	b := New(nil, nil, vm, nil, nil, nil, Config{DisableWarmBuilders: true}, nil)
	now := time.Unix(100, 0)
	if _, err := b.warm.Start(now, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	snapshot := testStorageWarmSnapshot()
	snapshot.ScopeKey = "current-scope"
	if err := b.warm.Complete(now, snapshot); err != nil {
		t.Fatal(err)
	}

	gotVM, result, gotSnapshot, started := b.prepareWarmBuilder(context.Background(), SlotDecision{Label: "guaranteed"}, VMRequest{WarmScopeKey: "current-scope"})
	if started || gotVM != nil || result != "" || gotSnapshot != (WarmSnapshot{}) {
		t.Fatalf("prepareWarmBuilder = (%v, %q, %+v, %v), want no warm builder", gotVM, result, gotSnapshot, started)
	}
	if len(vm.deleted) != 0 {
		t.Fatalf("disabled builderd touched retained snapshots: %+v", vm.deleted)
	}
}
