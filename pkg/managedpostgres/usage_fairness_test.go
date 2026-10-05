// adr: 565 — recover fleet usage before spending capacity on corrections.

package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func usageFleetFixture(t *testing.T, now time.Time, age time.Duration, count int) (*MemoryStore, *Registry, *usageTestProvider) {
	t.Helper()
	store, registry, provider := usageRecoveryFixture(t, now, age)
	base := store.databases["db"]
	delete(store.databases, "db")
	for i := 0; i < count; i++ {
		database := base
		database.ID = fmt.Sprintf("db-%03d", i)
		database.ProviderResourceID = database.ID
		store.databases[database.ID] = database
	}
	return store, registry, provider
}

func seedUsageFleetCoverage(t *testing.T, store UsageStore, databases []Database, from, to, observedAt time.Time) {
	t.Helper()
	for _, database := range databases {
		for start := from; start.Before(to); start = start.Add(time.Hour) {
			if err := store.RecordUsage(context.Background(), []UsageRecord{{
				AccountID: database.AccountID, DatabaseID: database.ID, BackendID: database.BackendID,
				BackendFingerprint: database.BackendFingerprint, WindowFrom: start, WindowTo: start.Add(time.Hour),
				ObservedAt: observedAt, Meter: MeterComputeUnitSeconds, Quantity: 60,
			}}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestUsageFleetRecoveryPrecedesOtherDatabaseCorrections(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageFleetFixture(t, now, 3*time.Hour, 20)
	to := now.Truncate(time.Hour)
	for id, database := range store.databases {
		if id != "db-019" {
			seedUsageFleetCoverage(t, store, []Database{database}, to.Add(-3*time.Hour), to, now)
		}
	}
	// A deterministic shared quota simulation, not live provider qualification.
	// Previously 19 caught-up databases exhausted it before the last database
	// got any recovery, on every replenished sweep.
	for sweep := 0; sweep < 3; sweep++ {
		attempts := 0
		provider.usageError = func(UsageWindow) error {
			attempts++
			if attempts > 50 {
				return ErrUnavailable
			}
			return nil
		}
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{BatchSize: 7, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		summary, err := collector.Collect(ctx)
		if !errors.Is(err, ErrUnavailable) || summary.Discovered != 20 {
			t.Fatalf("sweep %d: %+v, %v", sweep, summary, err)
		}
		progress, err := store.UsageProgress(ctx, "account", "db-019", time.Hour)
		if err != nil || !progress.CollectedUntil.Equal(to) {
			t.Fatalf("sweep %d: corrections displaced missing recovery: %+v, %v", sweep, progress, err)
		}
		now = now.Add(5 * time.Minute)
	}
	for i := 0; i < 3; i++ {
		if provider.usageCalls[i] != "db-019" {
			t.Fatalf("correction preceded recovery: %v", provider.usageCalls[:3])
		}
	}
}

func TestUsageFleetRecoveryGivesEveryDatabaseAWindowBeforeContinuing(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageFleetFixture(t, now, 49*time.Hour, 5)
	// A large outage on the first database must not consume the entire quota.
	attempts := 0
	provider.usageError = func(UsageWindow) error {
		attempts++
		if attempts > 5 {
			return ErrUnavailable
		}
		return nil
	}
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{BatchSize: 2, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := collector.Collect(context.Background())
	if !errors.Is(err, ErrUnavailable) || summary.Discovered != 5 || summary.Deferred != 5 {
		t.Fatalf("summary = %+v, %v", summary, err)
	}
	for id, database := range store.databases {
		progress, err := store.UsageProgress(context.Background(), database.AccountID, id, time.Hour)
		want := database.CreatedAt.Truncate(time.Hour).Add(time.Hour)
		if err != nil || !progress.CollectedUntil.Equal(want) {
			t.Fatalf("database %s lost its recovery turn: %+v, %v", id, progress, err)
		}
	}
}

func TestUsageFleetFairOrderSurvivesCollectorRestart(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageFleetFixture(t, now, 3*time.Hour, 8)
	for sweep := 0; sweep < 3; sweep++ {
		attempts := 0
		provider.usageError = func(UsageWindow) error {
			attempts++
			if attempts > 3 {
				return ErrUnavailable
			}
			return nil
		}
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{BatchSize: 2, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := collector.Collect(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
		now = now.Add(time.Minute)
	}
	for id := range store.databases {
		progress, err := store.UsageProgress(context.Background(), "account", id, time.Hour)
		if err != nil || progress.CollectedUntil.IsZero() {
			t.Fatalf("database %s starved across restarts: %+v, %v", id, progress, err)
		}
	}
}

func TestUsageFleetCancellationStopsRequestsAndReportsEachOutcomeOnce(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageFleetFixture(t, now, 3*time.Hour, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.usageError = func(UsageWindow) error {
		cancel()
		return context.Canceled
	}
	observations := make(map[string]int)
	var completed UsageCollectionSummary
	var completionErr error
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{
		BatchSize: 2, Now: func() time.Time { return now },
		Observe: func(observation UsageCollectionObservation) {
			observations[observation.DatabaseID]++
			if observation.Outcome != "deferred" {
				t.Errorf("canceled work outcome = %s", observation.Outcome)
			}
		},
		ObserveSweep: func(summary UsageCollectionSummary, err error) { completed, completionErr = summary, err },
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := collector.Collect(ctx)
	if !errors.Is(err, context.Canceled) || !errors.Is(completionErr, context.Canceled) ||
		summary != completed || summary.Discovered != 4 || summary.Deferred != 4 || summary.Recorded != 0 {
		t.Fatalf("canceled summary = %+v, %v; completion = %+v, %v", summary, err, completed, completionErr)
	}
	if len(provider.usageCalls) != 1 || len(store.usageProgress) != 0 {
		t.Fatalf("cancellation allowed more calls or committed coverage: calls=%v progress=%v", provider.usageCalls, store.usageProgress)
	}
	for id := range store.databases {
		if observations[id] != 1 {
			t.Fatalf("database %s observed %d times", id, observations[id])
		}
	}
}

func TestUsageFleetFailedDatabaseDoesNotBlockOtherRecovery(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageFleetFixture(t, now, 3*time.Hour, 4)
	provider.usageError = func(UsageWindow) error {
		if provider.usageCalls[len(provider.usageCalls)-1] == "db-000" {
			return ErrUnavailable
		}
		return nil
	}
	observations := make(map[string]string)
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{
		BatchSize: 2, Now: func() time.Time { return now },
		Observe: func(observation UsageCollectionObservation) {
			observations[observation.DatabaseID] = observation.Outcome
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := collector.Collect(context.Background())
	if !errors.Is(err, ErrUnavailable) || summary.Recorded != 3 || summary.Deferred != 1 || observations["db-000"] != "deferred" {
		t.Fatalf("summary = %+v, %v; observations = %v", summary, err, observations)
	}
	for id := range store.databases {
		if id != "db-000" && observations[id] != "recorded" {
			t.Fatalf("failed database blocked %s", id)
		}
	}
}

func TestPostgresUsageFleetFairRecoveryAcrossPagesAndRestart(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	_, registry, provider := usageRecoveryFixture(t, now, 3*time.Hour)
	backend := registry.backends["primary-a"]
	var databases []Database
	for i := 0; i < 8; i++ {
		database := postgresReadyDatabase(t, store, account, fmt.Sprintf("fair-recovery-%d", i), now.Add(-3*time.Hour))
		backend.Fingerprint = database.BackendFingerprint
		databases = append(databases, database)
	}
	registry.backends[backend.ID] = backend
	// Two caught-up databases must not replay ahead of six missing databases.
	to := now.Truncate(time.Hour)
	seedUsageFleetCoverage(t, store, databases[:2], to.Add(-3*time.Hour), to, now)
	for sweep := 0; sweep < 2; sweep++ {
		attempts := 0
		provider.usageError = func(UsageWindow) error {
			attempts++
			if attempts > 3 {
				return ErrUnavailable
			}
			return nil
		}
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{BatchSize: 2, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if summary, err := collector.Collect(ctx); !errors.Is(err, ErrUnavailable) || summary.Discovered != 8 {
			t.Fatalf("sweep %d: %+v, %v", sweep, summary, err)
		}
		now = now.Add(time.Minute)
	}
	for _, database := range databases[2:] {
		progress, err := store.UsageProgress(ctx, account, database.ID, time.Hour)
		if err != nil || progress.CollectedUntil.IsZero() {
			t.Fatalf("database %s starved across restart: %+v, %v", database.ID, progress, err)
		}
	}
	if err := registry.UsagePolicy().Admit(ctx, store, account, now); !errors.Is(err, ErrUsageStale) {
		t.Fatalf("partial recovery admitted more resources: %v", err)
	}
	provider.usageError = nil
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{BatchSize: 2, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := collector.Collect(ctx); err != nil || summary.Recorded != 8 {
		t.Fatalf("final recovery: %+v, %v", summary, err)
	}
	snapshot, err := store.UsageSnapshot(ctx, account, now)
	if err != nil || snapshot.ComputeUnitSeconds != 8*3*60 || snapshot.Stale(registry.UsagePolicy(), now) {
		t.Fatalf("ledger did not converge: %+v, %v", snapshot, err)
	}
}
