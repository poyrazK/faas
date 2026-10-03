// adr: 493 — bounded replay of late managed PostgreSQL usage corrections.

package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUsageCollectorReplaysRecentCorrectionsAcrossMonthBoundary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 0, 17, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 4*time.Hour)
	registry.usage.ComputeUnitHourMillicents = 3600
	collect := func() {
		t.Helper()
		// Replay must survive a collector restart without a local checkpoint.
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := collector.Collect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	oldEnd := now.Truncate(time.Hour)
	provider.usageReadings = func(window UsageWindow) []MeterReading {
		quantity := int64(60)
		switch {
		case window.From.Equal(oldEnd.Add(-time.Hour)):
			quantity = 99
		case window.From.Equal(oldEnd.Add(-2 * time.Hour)):
			quantity = 0 // A revision can remove consumption as well as add it.
		case window.From.Equal(oldEnd.Add(-3 * time.Hour)):
			quantity = 777 // Outside the replay horizon; must remain unchanged.
		}
		return []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: quantity}}
	}
	now = now.Add(time.Hour)
	collect()
	for _, period := range []struct {
		at       time.Time
		quantity int64
	}{{now, 60}, {now.AddDate(0, -1, 0), 219}} {
		snapshot, err := store.UsageSnapshot(ctx, "account", period.at)
		if err != nil || snapshot.ComputeUnitSeconds != period.quantity || snapshot.CostMillicents != period.quantity {
			t.Fatalf("period %s: snapshot = %+v, %v; want quantity and cost %d", period.at, snapshot, err, period.quantity)
		}
	}
	if len(provider.windows) != 7 || !provider.windows[4].From.Equal(oldEnd) {
		t.Fatalf("new window must precede bounded replay: %+v", provider.windows)
	}
	now = now.Add(time.Minute)
	collect()
	snapshot, err := store.UsageSnapshot(ctx, "account", now.AddDate(0, -1, 0))
	if err != nil || snapshot.ComputeUnitSeconds != 219 || len(store.usage) != 5 {
		t.Fatalf("replay double-counted or skipped corrected consumption: %+v, %v", snapshot, err)
	}
}

func TestUsageCorrectionsYieldToForwardRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 25*time.Hour)
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(ctx); !errors.Is(err, ErrUsageStale) {
		t.Fatalf("unfinished recovery = %v", err)
	}
	if len(provider.windows) != maximumUsageWindowsPerSweep {
		t.Fatalf("recovery exceeded request budget: %d", len(provider.windows))
	}
	to := now.Truncate(time.Hour)
	provider.usageReadings = func(window UsageWindow) []MeterReading {
		quantity := int64(60)
		if window.From.Equal(to.Add(-2 * time.Hour)) {
			quantity = 99
		}
		return []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: quantity}}
	}
	if _, err := collector.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if !provider.windows[24].From.Equal(to.Add(-time.Hour)) {
		t.Fatalf("replay displaced missing usage: %+v", provider.windows[24:])
	}
	snapshot, err := store.UsageSnapshot(ctx, "account", now)
	if err != nil || snapshot.ComputeUnitSeconds != 1539 || snapshot.Stale(registry.UsagePolicy(), now) {
		t.Fatalf("recovery missed a correction or coverage: %+v, %v", snapshot, err)
	}
}

func TestUsageCorrectionFailurePreservesCoverageAndRetries(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 17, 0, 0, time.UTC)
	store, registry, provider := usageRecoveryFixture(t, now, 3*time.Hour)
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	missing := now.Truncate(time.Hour).Add(-2 * time.Hour)
	provider.usageError = func(window UsageWindow) error {
		if window.From.Equal(missing) {
			return ErrUnavailable
		}
		return nil
	}
	now = now.Add(time.Minute)
	if summary, err := collector.Collect(ctx); !errors.Is(err, ErrUnavailable) || summary.Deferred != 1 {
		t.Fatalf("failed correction was hidden: %+v, %v", summary, err)
	}
	snapshot, err := store.UsageSnapshot(ctx, "account", now)
	if err != nil || snapshot.ComputeUnitSeconds != 180 || snapshot.Stale(registry.UsagePolicy(), now) {
		t.Fatalf("failed replay erased established coverage or ledger: %+v, %v", snapshot, err)
	}
	provider.usageError = nil
	provider.usageReadings = func(window UsageWindow) []MeterReading {
		quantity := int64(60)
		if window.From.Equal(missing) {
			quantity = 99
		}
		return []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: quantity}}
	}
	now = now.Add(time.Minute)
	if _, err := collector.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.UsageSnapshot(ctx, "account", now)
	if err != nil || snapshot.ComputeUnitSeconds != 219 || len(store.usage) != 3 {
		t.Fatalf("correction did not recover: %+v, %v", snapshot, err)
	}
}

func TestUsagePolicyRequiresWindowsThatPartitionUTCDays(t *testing.T) {
	for _, window := range []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour, 6 * time.Hour, 8 * time.Hour, 12 * time.Hour, 24 * time.Hour, 61 * time.Minute, time.Hour + time.Nanosecond, 5 * time.Hour, 7 * time.Hour, 13 * time.Hour, 23 * time.Hour} {
		t.Run(window.String(), func(t *testing.T) {
			valid := window%time.Hour == 0 && (24*time.Hour)%window == 0
			policy := enabledUsagePolicy()
			policy.Window = window
			policy.StaleAfter = 24 * time.Hour
			if err := policy.Validate(); (err == nil) != valid {
				t.Fatalf("policy window %s: %v; valid = %v", window, err, valid)
			}
			from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Truncate(window)
			_, err := validateUsageRecords([]UsageRecord{{AccountID: "account", DatabaseID: "db", BackendID: "primary-a",
				BackendFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				WindowFrom:         from, WindowTo: from.Add(window), ObservedAt: from.Add(window), Meter: MeterComputeUnitSeconds}})
			if (err == nil) != valid {
				t.Fatalf("ledger window %s: %v; valid = %v", window, err, valid)
			}
		})
	}
}

func TestUsageConfigRejectsDurationOverflow(t *testing.T) {
	for _, field := range []string{"collection interval", "staleness"} {
		t.Run(field, func(t *testing.T) {
			config := UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
				StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 100, MaxMonthlyComputeUnitSeconds: 1000,
				MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 20}
			if field == "collection interval" {
				config.CollectionIntervalSeconds = 18446744374 // Wraps to about 300 seconds.
			} else {
				config.StaleAfterSeconds = 18446754874 // Wraps to about 10800 seconds.
			}
			if _, err := config.policy(); err == nil {
				t.Fatalf("overflowing %s was accepted", field)
			}
		})
	}
}

func TestPostgresUsageReplayUpdatesLateCorrectionsAfterRestart(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 0, 17, 0, 0, time.UTC)
	_, registry, provider := usageRecoveryFixture(t, now, 3*time.Hour)
	registry.usage.ComputeUnitHourMillicents = 3600
	database := postgresReadyDatabase(t, store, account, "late-correction", now.Add(-3*time.Hour))
	backend := registry.backends[database.BackendID]
	backend.Fingerprint = database.BackendFingerprint
	registry.backends[backend.ID] = backend
	collect := func() {
		t.Helper()
		collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := collector.Collect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	collect()
	oldEnd := now.Truncate(time.Hour)
	provider.usageReadings = func(window UsageWindow) []MeterReading {
		quantity := int64(60)
		if window.From.Equal(oldEnd.Add(-time.Hour)) {
			quantity = 0
		} else if window.From.Equal(oldEnd.Add(-2 * time.Hour)) {
			quantity = 99
		}
		return []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: quantity}}
	}
	now = now.Add(time.Hour)
	collect()
	now = now.Add(time.Minute)
	collect()
	for _, period := range []struct {
		at       time.Time
		quantity int64
	}{{now, 60}, {now.AddDate(0, -1, 0), 159}} {
		snapshot, err := store.UsageSnapshot(ctx, account, period.at)
		if err != nil || snapshot.ComputeUnitSeconds != period.quantity || snapshot.CostMillicents != period.quantity || snapshot.Stale(registry.UsagePolicy(), now) {
			t.Fatalf("period %s: snapshot = %+v, %v; want %d", period.at, snapshot, err, period.quantity)
		}
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_postgres_usage WHERE database_id = $1`, database.ID).Scan(&rows); err != nil || rows != 4 {
		t.Fatalf("replay appended ledger rows: count=%d, %v", rows, err)
	}
}

func TestUsageStoresRejectUnsafeWindows(t *testing.T) {
	postgres, _, ctx, account := postgresStoreFixture(t)
	now := time.Date(2026, 10, 1, 12, 17, 0, 0, time.UTC)
	database := postgresReadyDatabase(t, postgres, account, "invalid-window", now.Add(-24*time.Hour))
	memory := NewMemoryStore()
	memory.databases[database.ID] = database
	for name, store := range map[string]UsageStore{"memory": memory, "postgres": postgres} {
		t.Run(name, func(t *testing.T) {
			for _, window := range []time.Duration{61 * time.Minute, 5 * time.Hour} {
				from := now.Truncate(window)
				err := store.RecordUsage(ctx, []UsageRecord{{AccountID: account, DatabaseID: database.ID, BackendID: database.BackendID,
					BackendFingerprint: database.BackendFingerprint, WindowFrom: from, WindowTo: from.Add(window), ObservedAt: now,
					Meter: MeterComputeUnitSeconds, Quantity: 60}})
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("%s ledger window: %v", window, err)
				}
				if err := store.RecordSharedUsage(ctx, account, database.ID, "missing-source", window); !errors.Is(err, ErrInvalid) {
					t.Fatalf("%s shared coverage window: %v", window, err)
				}
			}
			snapshot, err := store.UsageSnapshot(ctx, account, now)
			if err != nil || snapshot.ComputeUnitSeconds != 0 || !snapshot.Stale(enabledUsagePolicy(), now) {
				t.Fatalf("invalid windows committed evidence: %+v, %v", snapshot, err)
			}
		})
	}
}

type offsetUsageProgressStore struct {
	UsageStore
	location *time.Location
}

func (s offsetUsageProgressStore) UsageProgress(ctx context.Context, accountID, databaseID string, window time.Duration) (UsageProgress, error) {
	progress, err := s.UsageStore.UsageProgress(ctx, accountID, databaseID, window)
	progress.CollectedFrom = progress.CollectedFrom.In(s.location)
	progress.CollectedUntil = progress.CollectedUntil.In(s.location)
	return progress, err
}

func TestUsageCollectorResumesCheckpointsWithEquivalentTimezones(t *testing.T) {
	for _, location := range []*time.Location{time.Local, time.FixedZone("UTC+3", 3*60*60)} {
		t.Run(location.String(), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 2, 12, 17, 0, 0, time.UTC)
			store, registry, _ := usageRecoveryFixture(t, now, time.Hour)
			collector, err := NewUsageCollector(registry, offsetUsageProgressStore{store, location}, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.Collect(ctx); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Hour)
			if _, err := collector.Collect(ctx); err != nil {
				t.Fatalf("equivalent checkpoint timezone blocked collection: %v", err)
			}
			snapshot, err := store.UsageSnapshot(ctx, "account", now)
			if err != nil || snapshot.ComputeUnitSeconds != 120 || snapshot.Stale(registry.UsagePolicy(), now) {
				t.Fatalf("resumed usage = %+v, %v", snapshot, err)
			}
		})
	}
}
