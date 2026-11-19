package managedpostgres

import (
	"context"
	"testing"
	"time"
)

// production 2026-11-16: every database created in the still-open window failed
// the sweep with ErrUsageStale until the hour closed, which kept
// FaasManagedPostgresUsageCollectionDeferred firing for each new database.
// Nothing is due yet, so the sweep records it as current without a provider
// request, and collects its first window once that window closes.
func TestUsageCollectorTreatsADatabaseInItsFirstOpenWindowAsCurrent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 14, 40, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 10*time.Minute)
	var outcomes []string
	collect := func() UsageCollectionSummary {
		t.Helper()
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now },
			Observe: func(o UsageCollectionObservation) { outcomes = append(outcomes, o.Outcome) }})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := collector.Collect(ctx)
		if err != nil {
			t.Fatalf("sweep at %s: %v", now, err)
		}
		return summary
	}
	if summary := collect(); summary.Deferred != 0 || summary.Recorded != 1 || len(provider.windows) != 0 {
		t.Fatalf("summary = %+v, provider windows %v; want recorded with no provider request", summary, provider.windows)
	}
	if len(outcomes) != 1 || outcomes[0] != "recorded" {
		t.Fatalf("outcomes = %v, want [recorded]", outcomes)
	}
	now = now.Add(25 * time.Minute)
	if summary := collect(); summary.Deferred != 0 || len(provider.windows) != 1 ||
		!provider.windows[0].From.Equal(time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("after the window closed: summary = %+v, windows %v; want the first window collected", summary, provider.windows)
	}
}
