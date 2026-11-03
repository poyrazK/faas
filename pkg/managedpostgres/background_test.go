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
