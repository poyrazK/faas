// adr: 210
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Issue #3360: a secret or env change invalidates existing snapshots, but a
// live instance still holds the old environment. Parking it must not publish
// a fresh snapshot of that environment, or the next wake restores the value
// the customer replaced.
func TestPark_RuntimeConfigChange(t *testing.T) {
	cases := []struct {
		name          string
		changeBefore  bool // stamp the change before the instance starts
		changeAfter   bool // stamp the change after the instance starts
		wantState     state.State
		wantCaptures  int
		wantPublished int
	}{
		{name: "no change captures", wantState: state.StateParked, wantCaptures: 1, wantPublished: 1},
		{name: "change before start captures", changeBefore: true, wantState: state.StateParked, wantCaptures: 1, wantPublished: 1},
		{name: "change after start discards", changeAfter: true, wantState: state.StateStopped, wantCaptures: 0, wantPublished: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
			vmm := &fakeVMM{}
			notif := &fakeNotifier{}
			e := newEngine(t, store, vmm, notif, "1.10.0")
			if tc.changeBefore {
				if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
					t.Fatal(err)
				}
			}
			insID := primeRunPlusFrameworkReady(t, store, vmm, notif, e, app.ID, dep.ID)
			if tc.changeAfter {
				if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
					t.Fatal(err)
				}
			}
			capturesBefore := vmm.snapshots
			publishedBefore := notif.count(db.NotifySnapshotWritten)

			if err := e.Park(ctx, insID); err != nil {
				t.Fatalf("Park: %v", err)
			}

			if got := vmm.snapshots - capturesBefore; got != tc.wantCaptures {
				t.Errorf("init captures = %d, want %d", got, tc.wantCaptures)
			}
			if got := notif.count(db.NotifySnapshotWritten) - publishedBefore; got != tc.wantPublished {
				t.Errorf("snapshot_written = %d, want %d", got, tc.wantPublished)
			}
			ins, err := store.InstanceByID(ctx, insID)
			if err != nil {
				t.Fatalf("InstanceByID: %v", err)
			}
			if state.State(ins.State) != tc.wantState {
				t.Errorf("state = %q, want %q", ins.State, tc.wantState)
			}
		})
	}
}

// A change that lands while the capture is running must also keep the
// captured blob unpublished: apid's invalidation ran before the row existed.
func TestPark_RuntimeConfigChangeDuringCaptureIsNotPublished(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	enableWarmSnapshot(t, store, app.ID)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	insID := primeRunPlusFrameworkReady(t, store, vmm, notif, e, app.ID, dep.ID)
	vmm.warmSnapshotHook = func() {
		if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
			t.Errorf("MarkAppRuntimeConfigChanged: %v", err)
		}
	}
	publishedBefore := notif.count(db.NotifySnapshotWritten)

	if err := e.Park(ctx, insID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if vmm.warmSnapshots != 1 {
		t.Fatalf("warm captures = %d, want 1 (the change must land mid-capture)", vmm.warmSnapshots)
	}
	if got := notif.count(db.NotifySnapshotWritten) - publishedBefore; got != 0 {
		t.Fatalf("snapshot_written = %d, want 0 after a mid-capture config change", got)
	}
}

func TestInvalidateAppSnapshotsStampsRuntimeConfigChange(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 5)
	if _, ok, _ := store.AppRuntimeConfigChangedAt(ctx, app.ID); ok {
		t.Fatal("fresh app already has a runtime config change stamp")
	}
	if _, err := state.InvalidateAppSnapshots(ctx, store, app.ID); err != nil {
		t.Fatalf("InvalidateAppSnapshots: %v", err)
	}
	if _, ok, err := store.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || !ok {
		t.Fatalf("AppRuntimeConfigChangedAt = (ok=%v, err=%v), want stamped", ok, err)
	}
}

// changeLookupFailingStore fails the runtime-config stamp read, standing in
// for a database error during park.
type changeLookupFailingStore struct {
	*state.MemStore
}

func (changeLookupFailingStore) AppRuntimeConfigChangedAt(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, false, errors.New("database unavailable")
}

// A failed stamp read must skip the capture: a missed snapshot costs one
// cold boot, a stale one keeps a credential the customer replaced.
func TestPark_RuntimeConfigLookupFailureSkipsCapture(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	store := changeLookupFailingStore{MemStore: mem}
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	insID := primeRunPlusFrameworkReady(t, store, vmm, notif, e, app.ID, dep.ID)
	capturesBefore := vmm.snapshots
	publishedBefore := notif.count(db.NotifySnapshotWritten)

	if err := e.Park(ctx, insID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if got := vmm.snapshots - capturesBefore; got != 0 {
		t.Errorf("init captures = %d, want 0 when the change stamp cannot be read", got)
	}
	if got := notif.count(db.NotifySnapshotWritten) - publishedBefore; got != 0 {
		t.Errorf("snapshot_written = %d, want 0", got)
	}
	ins, err := store.InstanceByID(ctx, insID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if state.State(ins.State) != state.StateStopped {
		t.Errorf("state = %q, want %q", ins.State, state.StateStopped)
	}
}
