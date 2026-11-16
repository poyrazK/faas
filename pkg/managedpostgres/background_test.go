package managedpostgres

import (
	"context"
	"testing"
	"time"
)

// The usage collector's provider requests carry the background mark, so a
// provider can pace them behind customer actions.
func TestUsageCollectionMarksItsRequestsAsBackground(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 7, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 3*time.Hour)
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.usageCalls) == 0 || provider.backgroundCalls != len(provider.usageCalls) {
		t.Fatalf("background calls = %d of %d", provider.backgroundCalls, len(provider.usageCalls))
	}
	if BackgroundRequests(context.Background()) || !BackgroundRequests(WithBackgroundRequests(context.Background())) {
		t.Fatal("background mark misread")
	}
}

// A database created inside the still-open window owes no usage yet. The
// sweep deferred it as stale until the hour ended, which failed every sweep
// and fired FaasManagedPostgresUsageCollectionDeferred for each new database
// (prod 2026-11-15).
func TestUsageCollectionDoesNotDeferANewDatabase(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 47, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 20*time.Minute)
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := collector.Collect(context.Background())
	if err != nil || summary.Deferred != 0 || len(provider.usageCalls) != 0 {
		t.Fatalf("new database sweep = %+v, %v, provider calls %d", summary, err, len(provider.usageCalls))
	}
	// Once its first window closes, the uncollected window is owed.
	later := now.Add(time.Hour)
	collector, err = NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return later }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(context.Background()); err != nil || len(provider.usageCalls) == 0 {
		t.Fatalf("first closed window not collected: %v, provider calls %d", err, len(provider.usageCalls))
	}
}
