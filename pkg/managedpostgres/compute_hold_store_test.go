// adr: 1027 — an operational hold lease preserves lifecycle state and pins.
package managedpostgres

import (
	"errors"
	"testing"
	"time"
)

type computeHoldFixture struct {
	store    Store
	holds    ComputeHoldStore
	database Database
	now      time.Time
}

func TestComputeHoldMemoryLease(t *testing.T) {
	testComputeHoldLease(t, func(t *testing.T) computeHoldFixture {
		f := newGraceFixture(t, newHoldingProvider())
		return computeHoldFixture{f.store, f.store, f.readyDatabase(t, "hold-lease"), *f.clock}
	})
}

func testComputeHoldLease(t *testing.T, fixture func(*testing.T) computeHoldFixture) {
	t.Run("serializes deletion and preserves state", func(t *testing.T) {
		f := fixture(t)
		d := f.database
		claimed, err := f.holds.ClaimComputeHold(t.Context(), d, "cap", f.now, f.now.Add(time.Minute))
		if err != nil || claimed.State != d.State || claimed.LastErrorCode != d.LastErrorCode || !claimed.RetryAt.Equal(d.RetryAt) || claimed.DesiredGeneration != d.DesiredGeneration || !claimed.UpdatedAt.Equal(d.UpdatedAt) {
			t.Fatalf("claim changed lifecycle state: %+v err=%v", claimed, err)
		}
		deletes := f.store.(DeletionScheduleStore)
		if _, err := deletes.ClaimScheduledDelete(t.Context(), d.AccountID, d.ID, "delete", f.now, f.now.Add(time.Minute), f.now.Add(time.Hour)); !errors.Is(err, ErrConflict) {
			t.Fatal("deletion bypassed live cap lease", err)
		}
		if err := f.holds.ReleaseComputeHold(t.Context(), d.ID, "cap", f.now); err != nil {
			t.Fatal(err)
		}
		if _, err := deletes.ClaimScheduledDelete(t.Context(), d.AccountID, d.ID, "delete", f.now, f.now.Add(time.Minute), f.now.Add(time.Hour)); err != nil {
			t.Fatal("delete after cap completion", err)
		}
		if err := f.holds.ReleaseComputeHold(t.Context(), d.ID, "cap", f.now); !errors.Is(err, ErrConflict) {
			t.Fatal("old cap completion changed a deleting database", err)
		}
	})
	t.Run("rejects stale pins and deleting rows", func(t *testing.T) {
		f := fixture(t)
		for _, change := range []func(*Database){
			func(d *Database) { d.State = StateFailed },
			func(d *Database) { d.DesiredGeneration++ },
			func(d *Database) { d.BackendID += "-other" },
			func(d *Database) { d.DataResourceID = "replacement" },
			func(d *Database) { d.ProviderResourceID += "-other" },
		} {
			stale := f.database
			change(&stale)
			if _, err := f.holds.ClaimComputeHold(t.Context(), stale, "stale", f.now, f.now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale pins accepted: %+v err=%v", stale, err)
			}
		}
		if _, err := f.store.(DeletionScheduleStore).ClaimScheduledDelete(t.Context(), f.database.AccountID, f.database.ID, "delete", f.now, f.now.Add(time.Minute), f.now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.holds.ClaimComputeHold(t.Context(), f.database, "stale", f.now, f.now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
			t.Fatal("cap claim accepted a stale ready snapshot", err)
		}
	})
	t.Run("expired owner cannot release replacement", func(t *testing.T) {
		f := fixture(t)
		if _, err := f.holds.ClaimComputeHold(t.Context(), f.database, "expired", f.now, f.now.Add(10*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond) // PostgreSQL's authoritative lease clock
		later := f.now.Add(time.Second)
		if _, err := f.holds.ClaimComputeHold(t.Context(), f.database, "replacement", later, later.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := f.holds.ReleaseComputeHold(t.Context(), f.database.ID, "expired", later); !errors.Is(err, ErrConflict) {
			t.Fatal("expired worker released replacement lease", err)
		}
		if err := f.holds.ReleaseComputeHold(t.Context(), f.database.ID, "replacement", later); err != nil {
			t.Fatal(err)
		}
	})
}
