package managedpostgres

import (
	"context"
	"testing"
	"time"
)

// production 2026-11-17: a journey database created at 18:50 was deleted at
// 19:02, before the sweep had collected its first window. The sweep refused
// the database outright while its final window (19:00–20:00) was open, so it
// had no coverage at all, admission read that as missing coverage, and the
// account's restores, creates and resizes were refused until about 20:05.
// The closed window is collected at once; only the final window waits.
func TestDeletedDatabaseCollectsItsClosedWindowsWhileTheFinalWindowIsOpen(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 19, 5, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 15*time.Minute)
	deleted := time.Date(2026, 11, 17, 19, 2, 0, 0, time.UTC)
	database := store.databases["db"]
	database.State, database.DeletedAt = StateDeleted, &deleted
	store.databases["db"] = database

	collect := func() UsageCollectionSummary {
		t.Helper()
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := collector.Collect(ctx)
		if err != nil {
			t.Fatalf("sweep at %s: %v", now, err)
		}
		return summary
	}
	if summary := collect(); summary.Deferred != 0 || len(provider.windows) != 1 ||
		!provider.windows[0].From.Equal(time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)) {
		t.Fatalf("summary = %+v, windows %v; want the closed 18:00 window and nothing from the open one", summary, provider.windows)
	}
	snapshot, err := store.UsageSnapshot(ctx, "account", now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Stale(registry.UsagePolicy(), now) {
		t.Fatal("the account is stale while only the deleted database's final window is open")
	}

	now = time.Date(2026, 11, 17, 20, 5, 0, 0, time.UTC)
	collect()
	if len(provider.windows) < 2 || !provider.windows[1].From.Equal(time.Date(2026, 11, 17, 19, 0, 0, 0, time.UTC)) {
		t.Fatalf("windows %v; want the final 19:00 window once it closed", provider.windows)
	}
}
