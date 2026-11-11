// adr: 1027 — compute holds and quotas (amendment 2026-11-10): a held
// database, or one past its monthly compute quota, is parked and its wakes
// are refused; releasing it, raising the quota or a new month lets it wake.
package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHoldParksAndRefusesWakes(t *testing.T) {
	ctx := context.Background()
	s, store, vmm := newTestScheduler(t, 4096)
	db := provisionAlwaysOn(t, s)
	if _, err := s.Wake(ctx, db.DatabaseID); err != nil {
		t.Fatal(err)
	}

	held, err := s.SetHold(ctx, db.DatabaseID, true)
	if err != nil || !held.Held || held.State != StateParked {
		t.Fatalf("hold = %+v, %v; want held and parked", held, err)
	}
	if _, err := s.Wake(ctx, db.DatabaseID); !errors.Is(err, ErrHeld) {
		t.Fatalf("wake while held = %v, want ErrHeld", err)
	}
	if _, err := s.Admin(ctx, db.DatabaseID, "SELECT 1"); !errors.Is(err, ErrHeld) {
		t.Fatalf("admin while held = %v, want ErrHeld", err)
	}
	starts := vmm.callCount("start")
	if n, err := s.KeepWarm(ctx); err != nil || n != 0 || vmm.callCount("start") != starts {
		t.Fatalf("keep-warm woke a held always-on database: %d, %v", n, err)
	}

	released, err := s.SetHold(ctx, db.DatabaseID, false)
	if err != nil || released.Held {
		t.Fatalf("release = %+v, %v", released, err)
	}
	if got, err := s.Wake(ctx, db.DatabaseID); err != nil || got.State != StateRunning {
		t.Fatalf("wake after release = %+v, %v", got, err)
	}
	if _, err := store.Get(ctx, db.DatabaseID); err != nil {
		t.Fatal(err)
	}
}

// A wake another replica admitted before the hold landed leaves a held
// database running; the idle loop's sweep parks it.
func TestParkHeldParksRunningHeldDatabases(t *testing.T) {
	ctx := context.Background()
	s, store, _ := newTestScheduler(t, 4096)
	held, free := provision(t, s), provision(t, s)
	for _, db := range []VM{held, free} {
		if _, err := s.Wake(ctx, db.DatabaseID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetHeld(ctx, held.DatabaseID, true); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ParkHeld(ctx); err != nil || n != 1 {
		t.Fatalf("ParkHeld = %d, %v; want 1", n, err)
	}
	for id, want := range map[string]State{held.DatabaseID: StateParked, free.DatabaseID: StateRunning} {
		if got, _ := store.Get(ctx, id); got.State != want {
			t.Errorf("%s = %s, want %s", id, got.State, want)
		}
	}
}

func TestComputeQuotaRefusesPastTheMonthsUse(t *testing.T) {
	ctx := context.Background()
	s, store, _ := newTestScheduler(t, 4096)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	clock := month.Add(10 * 24 * time.Hour)
	now := func() time.Time { return clock }
	store.now, s.now = now, now
	db := provision(t, s) // 1 vCPU: one compute-unit second per second

	if _, err := s.Wake(ctx, db.DatabaseID); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(90 * time.Second)
	used, start, err := s.ComputeThisMonth(ctx, db.DatabaseID)
	if err != nil || used != 90 || !start.Equal(month) {
		t.Fatalf("ComputeThisMonth = %d from %s, %v; want 90 from %s", used, start, err, month)
	}

	// A quota above the month's use leaves the database running.
	if got, err := s.SetComputeQuota(ctx, db.DatabaseID, 120); err != nil || got.State != StateRunning || got.ComputeQuotaUnitSeconds != 120 {
		t.Fatalf("quota 120 = %+v, %v", got, err)
	}
	if n, _ := s.ParkHeld(ctx); n != 0 {
		t.Fatalf("parked a database under its quota (%d)", n)
	}
	// Running past it, connections still reach it until the sweep parks
	// it; then wakes are refused.
	clock = clock.Add(time.Minute)
	if got, err := s.Wake(ctx, db.DatabaseID); err != nil || got.State != StateRunning {
		t.Fatalf("wake of a running database past its quota = %+v, %v; want it served until the sweep", got, err)
	}
	if n, err := s.ParkHeld(ctx); err != nil || n != 1 {
		t.Fatalf("ParkHeld past the quota = %d, %v", n, err)
	}
	if _, err := s.Wake(ctx, db.DatabaseID); !errors.Is(err, ErrComputeQuota) {
		t.Fatalf("wake past the quota = %v, want ErrComputeQuota", err)
	}
	// Lowering a quota under a running database's use parks it at once.
	if _, err := s.SetComputeQuota(ctx, db.DatabaseID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Wake(ctx, db.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SetComputeQuota(ctx, db.DatabaseID, 100); err != nil || got.State != StateParked {
		t.Fatalf("quota under the use = %+v, %v; want parked", got, err)
	}
	// The next month's use starts at zero, so the quota no longer refuses.
	clock = month.AddDate(0, 1, 0).Add(time.Minute)
	if got, err := s.Wake(ctx, db.DatabaseID); err != nil || got.State != StateRunning {
		t.Fatalf("wake in the next month = %+v, %v", got, err)
	}
}

func TestHoldAndQuotaRefuseDeletingDatabases(t *testing.T) {
	ctx := context.Background()
	s, store, _ := newTestScheduler(t, 4096)
	if _, err := s.SetHold(ctx, "00000000-0000-0000-0000-000000000000", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hold of a missing database = %v", err)
	}
	db := provision(t, s)
	if _, err := s.SetComputeQuota(ctx, db.DatabaseID, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative quota = %v", err)
	}
	if err := store.Transition(ctx, db.DatabaseID, []State{StateParked}, StateDeleting, TransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetHold(ctx, db.DatabaseID, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("hold of a deleting database = %v", err)
	}
	if _, err := s.SetComputeQuota(ctx, db.DatabaseID, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("quota of a deleting database = %v", err)
	}
}
