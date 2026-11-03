// adr: 20261029174950 — decision 6: usage while a delete is scheduled is
// waived for the customer and still counted by the fleet spend breaker.
// adr: 20261103091657 — a database with restore copies is not waived: its
// usage includes theirs.
package managedpostgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type usageWaiverTestStore interface {
	Store
	UsageStore
	DeletionScheduleStore
	FleetTotals(context.Context, time.Time) (FleetTotals, error)
}

func TestMemoryStoreWaivesUsageWhileDeleteIsScheduled(t *testing.T) {
	testUsageWaiver(t, NewMemoryStore(), uuid.NewString())
}

func TestPostgresStoreWaivesUsageWhileDeleteIsScheduled(t *testing.T) {
	store, _, _, accountID := postgresStoreFixture(t)
	testUsageWaiver(t, store, accountID)
}

// testUsageWaiver schedules, undoes, schedules again and purges, with nine
// hourly windows of usage across them. A window counts for the customer unless
// it lies wholly inside a scheduled window.
func testUsageWaiver(t *testing.T, store usageWaiverTestStore, accountID string) {
	t.Helper()
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	at := func(hours, minutes int) time.Time {
		return start.Add(time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute)
	}
	database := usageWaiverReadyDatabase(t, store, accountID, start)
	for k := range 9 {
		quantity := int64(1) << k
		if err := store.RecordUsage(ctx, []UsageRecord{{
			AccountID: accountID, DatabaseID: database.ID, BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
			WindowFrom: at(k, 0), WindowTo: at(k+1, 0), ObservedAt: at(k+1, 1),
			Meter: MeterComputeUnitSeconds, Quantity: quantity, CostMillicents: quantity,
		}}); err != nil {
			t.Fatalf("record window %d: %v", k, err)
		}
	}
	purgeAfter := at(7*24, 0)
	schedule := func(lease string, now time.Time) {
		t.Helper()
		if _, err := store.ClaimScheduledDelete(ctx, accountID, database.ID, lease, now, now.Add(time.Minute), purgeAfter); err != nil {
			t.Fatalf("schedule at %s: %v", now, err)
		}
		if err := store.Release(ctx, database.ID, lease, StateDeleting, "", now, purgeAfter); err != nil {
			t.Fatalf("release schedule at %s: %v", now, err)
		}
	}
	schedule("schedule-1", at(1, 30))
	if _, err := store.ClaimUndelete(ctx, accountID, database.ID, "undo", at(4, 30), at(4, 31)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelScheduledDelete(ctx, database.ID, "undo", at(4, 30)); err != nil {
		t.Fatal(err)
	}
	schedule("schedule-2", at(6, 0))
	if _, err := store.ClaimDelete(ctx, accountID, database.ID, "purge", at(8, 0), at(8, 1)); err != nil {
		t.Fatal(err)
	}

	// Waived: 14–16 (inside 13:30–16:30) and 18–20 (inside 18:00–20:00). The
	// windows that straddle a schedule or an undo still count.
	const customer = 1<<0 + 1<<1 + 1<<4 + 1<<5 + 1<<8
	snapshot, err := store.UsageSnapshot(ctx, accountID, start)
	if err != nil || snapshot.ComputeUnitSeconds != customer || snapshot.CostMillicents != customer {
		t.Fatalf("customer usage = %d units, %d millicents, %v; want %d", snapshot.ComputeUnitSeconds, snapshot.CostMillicents, err, customer)
	}
	if totals, err := store.FleetTotals(ctx, start); err != nil || totals.CostMillicents < 1<<9-1 {
		t.Fatalf("fleet cost = %d, %v; want the waived windows counted (at least %d)", totals.CostMillicents, err, 1<<9-1)
	}
}

func usageWaiverReadyDatabase(t *testing.T, store Store, accountID string, at time.Time) Database {
	t.Helper()
	ctx := context.Background()
	database, created, err := store.Reserve(ctx, postgresTestDatabase(accountID, "waiver", at), 100)
	if err != nil || !created {
		t.Fatalf("reserve: created=%v %v", created, err)
	}
	claimed, err := store.Claim(ctx, accountID, database.ID, uuid.NewString(), StateProvisioning, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claimed.LeaseToken, "provider-"+database.ID, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ready, err := store.FinishProvision(ctx, database.ID, claimed.LeaseToken, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return ready
}

func TestMemoryStoreDoesNotWaiveARestoreSource(t *testing.T) {
	testUsageWaiverSkipsRestoreSources(t, NewMemoryStore(), uuid.NewString())
}

func TestPostgresStoreDoesNotWaiveARestoreSource(t *testing.T) {
	store, _, _, accountID := postgresStoreFixture(t)
	testUsageWaiverSkipsRestoreSources(t, store, accountID)
}

// testUsageWaiverSkipsRestoreSources schedules the delete of a database with
// a restore copy: every window keeps counting, because the source's usage is
// the copy's too.
func testUsageWaiverSkipsRestoreSources(t *testing.T, store usageWaiverTestStore, accountID string) {
	t.Helper()
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	database := usageWaiverReadyDatabase(t, store, accountID, start)
	restored := postgresTestDatabase(accountID, "waiver-copy", start.Add(time.Minute))
	restored.RestoreSourceDatabaseID = database.ID
	restored.RestoreSourceResourceID = database.ProviderResourceID
	restored.RestorePointInTime = start
	if _, _, err := store.Reserve(ctx, restored, 100); err != nil {
		t.Fatalf("reserve copy: %v", err)
	}
	for k := range 4 {
		if err := store.RecordUsage(ctx, []UsageRecord{{
			AccountID: accountID, DatabaseID: database.ID, BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
			WindowFrom: start.Add(time.Duration(k) * time.Hour), WindowTo: start.Add(time.Duration(k+1) * time.Hour), ObservedAt: start.Add(time.Duration(k+1)*time.Hour + time.Minute),
			Meter: MeterComputeUnitSeconds, Quantity: 1, CostMillicents: 1,
		}}); err != nil {
			t.Fatalf("record window %d: %v", k, err)
		}
	}
	now := start.Add(30 * time.Minute)
	if _, err := store.ClaimScheduledDelete(ctx, accountID, database.ID, "schedule", now, now.Add(time.Minute), start.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("schedule a source with a copy: %v", err)
	}
	snapshot, err := store.UsageSnapshot(ctx, accountID, start)
	if err != nil || snapshot.ComputeUnitSeconds != 4 {
		t.Fatalf("customer usage = %d, %v; want all 4 windows (3 would be waived)", snapshot.ComputeUnitSeconds, err)
	}
}
