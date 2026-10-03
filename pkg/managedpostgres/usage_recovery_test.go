package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresUsageDeletionRetainsMonthlyConsumption(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	database := postgresReadyDatabase(t, store, account, "audit-deleted", now)
	err := store.RecordUsage(ctx, []UsageRecord{{AccountID: account, DatabaseID: database.ID,
		BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
		WindowFrom: now.Add(-time.Hour), WindowTo: now, ObservedAt: now,
		Meter: MeterComputeUnitSeconds, Quantity: 60, CostMillicents: 60}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.UsageSnapshot(ctx, account, now)
	if err != nil || before.ComputeUnitSeconds != 60 {
		t.Fatalf("before: %+v, %v", before, err)
	}
	claimed, err := store.ClaimDelete(ctx, account, database.ID, "audit-delete-lease", now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishDelete(ctx, database.ID, claimed.LeaseToken, now.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := store.UsageSnapshot(ctx, account, now)
	if err != nil {
		t.Fatal(err)
	}
	if after.ComputeUnitSeconds != before.ComputeUnitSeconds || after.CostMillicents != before.CostMillicents {
		t.Fatalf("deletion removed historical monthly usage: before=%+v after=%+v", before, after)
	}
}

func TestPostgresUsageFreshDatabaseDoesNotHideUnmeteredDatabase(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	first := postgresReadyDatabase(t, store, account, "audit-fresh", now)
	_ = postgresReadyDatabase(t, store, account, "audit-unmetered", now)
	if err := store.RecordUsage(ctx, []UsageRecord{{AccountID: account, DatabaseID: first.ID,
		BackendID: first.BackendID, BackendFingerprint: first.BackendFingerprint,
		WindowFrom: now.Add(-time.Hour), WindowTo: now, ObservedAt: now,
		Meter: MeterComputeUnitSeconds, Quantity: 1, CostMillicents: 1}}); err != nil {
		t.Fatal(err)
	}
	err := enabledUsagePolicy().Admit(ctx, store, account, now)
	if !errors.Is(err, ErrUsageStale) {
		t.Fatalf("account with unmetered ready database admitted: %v", err)
	}
}

func TestUsageCollectorBackfillsOutageWindows(t *testing.T) {
	provider := &usageTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 60}}}
	registry := testRegistry(t, provider, func(config *Config) {
		config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 100000,
			MaxMonthlyComputeUnitSeconds: 100000, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
	})
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 10, 17, 0, 0, time.UTC)
	store := NewMemoryStore()
	store.databases["db"] = Database{ID: "db", AccountID: "account", Name: "audit", State: StateReady,
		Spec: testSpec(), BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		ProviderResourceID: "provider-db", CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Hour)
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.UsageSnapshot(context.Background(), "account", now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ComputeUnitSeconds != 240 {
		t.Fatalf("outage left usage windows missing: got %d CU-seconds, want 240", snapshot.ComputeUnitSeconds)
	}
}

func usageRecoveryFixture(t *testing.T, now time.Time, age time.Duration) (*MemoryStore, *Registry, *usageTestProvider) {
	t.Helper()
	provider := &usageTestProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}, readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 60}}}
	registry := testRegistry(t, provider, func(config *Config) {
		config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
			MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40}
	})
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	store.databases["db"] = Database{ID: "db", AccountID: "account", Name: "orders", State: StateReady,
		Spec: testSpec(), BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		ProviderResourceID: "provider-db", CreatedAt: now.Add(-age), UpdatedAt: now}
	return store, registry, provider
}

func TestUsageRecoveryResumesAcrossCollectorRestartAndMonthBoundary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 49*time.Hour)
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for sweep := 0; sweep < 3; sweep++ {
		// Reconstructing the collector must use persisted progress, not local state.
		collector, err = NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		_, err = collector.Collect(ctx)
		if sweep < 2 && !errors.Is(err, ErrUsageStale) {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
		if sweep == 2 && err != nil {
			t.Fatal(err)
		}
		snapshot, snapshotErr := store.UsageSnapshot(ctx, "account", now)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if snapshot.Stale(registry.UsagePolicy(), now) != (sweep < 2) {
			t.Fatalf("sweep %d freshness: %+v", sweep, snapshot)
		}
	}
	if len(provider.usageCalls) != 49 {
		t.Fatalf("calls = %d, want 49", len(provider.usageCalls))
	}
	snapshot, err := store.UsageSnapshot(ctx, "account", now)
	if err != nil || snapshot.ComputeUnitSeconds != 12*60 {
		t.Fatalf("current month = %+v, %v", snapshot, err)
	}
	snapshot, err = store.UsageSnapshot(ctx, "account", now.AddDate(0, -1, 0))
	if err != nil || snapshot.ComputeUnitSeconds != 37*60 {
		t.Fatalf("previous month = %+v, %v", snapshot, err)
	}
}

func TestUsageRecoveryStopsAtMissingWindowAndRetriesIt(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 3*time.Hour)
	missing := now.Truncate(time.Hour).Add(-2 * time.Hour)
	provider.usageError = func(window UsageWindow) error {
		if window.From.Equal(missing) {
			return ErrUnavailable
		}
		return nil
	}
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("collect = %v", err)
	}
	progress, err := store.UsageProgress(ctx, "account", "db", time.Hour)
	if err != nil || !progress.CollectedUntil.Equal(missing) {
		t.Fatalf("progress = %+v, %v", progress, err)
	}
	provider.usageError = nil
	if _, err := collector.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if !provider.windows[2].From.Equal(missing) {
		t.Fatalf("retry skipped missing window: %+v", provider.windows)
	}
	snapshot, err := store.UsageSnapshot(ctx, "account", now)
	if err != nil || snapshot.ComputeUnitSeconds != 180 {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
}

func TestUsageCollectorRejectsPartialProviderMeters(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, time.Hour)
	// Registry capabilities are immutable; a partial response cannot claim coverage.
	backend := registry.backends[store.databases["db"].BackendID]
	backend.Capabilities.UsageMeters = append(backend.Capabilities.UsageMeters, MeterStorageByteSeconds)
	registry.backends[backend.ID] = backend
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("collect = %v", err)
	}
	if len(store.usage) != 0 || len(store.usageProgress) != 0 {
		t.Fatal("partial response committed usage or coverage")
	}
	provider.readings = append(provider.readings, MeterReading{Meter: MeterStorageByteSeconds, Quantity: 0})
	if _, err := collector.Collect(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUsageCoverageSerializesConcurrentCorrections(t *testing.T) {
	store, _, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	database := postgresReadyDatabase(t, store, account, "concurrent-usage", now)
	record := UsageRecord{AccountID: account, DatabaseID: database.ID, BackendID: database.BackendID,
		BackendFingerprint: database.BackendFingerprint, WindowFrom: now.Add(-time.Hour), WindowTo: now,
		ObservedAt: now, Meter: MeterComputeUnitSeconds, Quantity: 60, CostMillicents: 60}
	errorsCh := make(chan error, 8)
	for i := 0; i < cap(errorsCh); i++ {
		go func() { errorsCh <- store.RecordUsage(ctx, []UsageRecord{record}) }()
	}
	for i := 0; i < cap(errorsCh); i++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := store.UsageSnapshot(ctx, account, now)
	if err != nil || snapshot.ComputeUnitSeconds != 60 || snapshot.Stale(enabledUsagePolicy(), now) {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	// A malformed second reading must roll back the entire corrected window.
	record.Quantity = 99
	invalid := record
	invalid.Meter = MeterEgressBytes
	invalid.Quantity = -1
	if err := store.RecordUsage(ctx, []UsageRecord{record, invalid}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid batch = %v", err)
	}
	snapshot, err = store.UsageSnapshot(ctx, account, now)
	if err != nil || snapshot.ComputeUnitSeconds != 60 {
		t.Fatalf("partial batch changed ledger: %+v, %v", snapshot, err)
	}
}

func TestUsageStoresRejectOverlappingWindowSizesAndOlderCorrections(t *testing.T) {
	postgres, _, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	database := postgresReadyDatabase(t, postgres, account, "window-size", now.Add(-2*time.Hour))
	memory := NewMemoryStore()
	memory.databases[database.ID] = database
	for name, store := range map[string]UsageStore{"memory": memory, "postgres": postgres} {
		t.Run(name, func(t *testing.T) {
			record := UsageRecord{AccountID: account, DatabaseID: database.ID, BackendID: database.BackendID,
				BackendFingerprint: database.BackendFingerprint, WindowFrom: now.Add(-2 * time.Hour), WindowTo: now.Add(-time.Hour),
				ObservedAt: now, Meter: MeterComputeUnitSeconds, Quantity: 99, CostMillicents: 99}
			if err := store.RecordUsage(ctx, []UsageRecord{record}); err != nil {
				t.Fatal(err)
			}
			record.ObservedAt = now.Add(-time.Minute)
			record.Quantity = 60
			if err := store.RecordUsage(ctx, []UsageRecord{record}); err != nil {
				t.Fatal(err)
			}
			record.WindowTo = now
			if err := store.RecordUsage(ctx, []UsageRecord{record}); !errors.Is(err, ErrConflict) {
				t.Fatalf("overlapping window admitted: %v", err)
			}
			snapshot, err := store.UsageSnapshot(ctx, account, now)
			if err != nil || snapshot.ComputeUnitSeconds != 99 {
				t.Fatalf("snapshot = %+v, %v", snapshot, err)
			}
		})
	}
}

func TestPostgresRestoreUsageCoverageFollowsSource(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	source := postgresReadyDatabase(t, store, account, "shared-source", now)
	target := postgresReadyDatabase(t, store, account, "shared-target", now)
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET restore_source_database_id = $1, restore_source_resource_id = $2, restore_point_in_time = $3 WHERE id = $4`, source.ID, source.ProviderResourceID, now.Add(-time.Minute), target.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSharedUsage(ctx, account, target.ID, source.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.UsageSnapshot(ctx, account, now)
	if err != nil || !snapshot.Stale(enabledUsagePolicy(), now) {
		t.Fatalf("unmetered source admitted: %+v, %v", snapshot, err)
	}
	if err := store.RecordUsage(ctx, []UsageRecord{{AccountID: account, DatabaseID: source.ID, BackendID: source.BackendID,
		BackendFingerprint: source.BackendFingerprint, WindowFrom: now.Add(-time.Hour), WindowTo: now, ObservedAt: now,
		Meter: MeterComputeUnitSeconds, Quantity: 60, CostMillicents: 60}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.UsageSnapshot(ctx, account, now)
	if err != nil || snapshot.ReadyDatabases != 2 || snapshot.ComputeUnitSeconds != 60 || snapshot.Stale(enabledUsagePolicy(), now) {
		t.Fatalf("shared source = %+v, %v", snapshot, err)
	}
	if !snapshot.Stale(enabledUsagePolicy(), now.Add(time.Hour)) {
		t.Fatal("shared source hid a missing window")
	}
}

func TestPostgresUsageCoverageRejectsIncompleteCheckpoint(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	database := postgresReadyDatabase(t, store, account, "coverage-constraint", now)
	for _, test := range []struct {
		name                  string
		from, until, observed any
	}{
		{name: "missing until", from: now.Add(-time.Hour), observed: now},
		{name: "missing from", until: now, observed: now},
		{name: "missing observation", from: now.Add(-time.Hour), until: now},
		{name: "empty interval", from: now, until: now, observed: now},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_usage_coverage (database_id, window_seconds, collected_from, collected_until, observed_at) VALUES ($1, 3600, $2, $3, $4)`, database.ID, test.from, test.until, test.observed); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
}
