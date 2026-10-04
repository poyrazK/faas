// adr: 569 — retain finite accounting through provider-confirmed shutdown.

package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type retirementUsageStore interface {
	Store
	UsageStore
}

func retirementFixture(t *testing.T, kind string, createdAt time.Time) (retirementUsageStore, *Registry, *usageTestProvider, Database) {
	t.Helper()
	_, registry, provider := usageRecoveryFixture(t, createdAt, time.Hour)
	var store retirementUsageStore = NewMemoryStore()
	account := uuid.NewString()
	if kind == "postgres" {
		store, _, _, account = postgresStoreFixture(t)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	input := postgresTestDatabase(account, "retirement", createdAt)
	input.BackendID, input.BackendFingerprint = backend.ID, backend.Fingerprint
	database := retirementReadyDatabase(t, store, input)
	return store, registry, provider, database
}

func retirementReadyDatabase(t *testing.T, store retirementUsageStore, input Database) Database {
	t.Helper()
	ctx := context.Background()
	database, _, err := store.Reserve(ctx, input, 100)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, database.AccountID, database.ID, uuid.NewString(), StateProvisioning, input.CreatedAt, input.CreatedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claimed.LeaseToken, "provider-"+database.ID, input.CreatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	database, err = store.FinishProvision(ctx, database.ID, claimed.LeaseToken, input.CreatedAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func retirementDelete(t *testing.T, store retirementUsageStore, database Database, at time.Time, finish bool) Database {
	t.Helper()
	ctx := context.Background()
	claimed, err := store.ClaimDelete(ctx, database.AccountID, database.ID, uuid.NewString(), at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !finish {
		return claimed
	}
	deleted, err := store.FinishDelete(ctx, database.ID, claimed.LeaseToken, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return deleted
}

func TestUsageKnownResourcesRemainAccountableOutsideReadyState(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		for _, state := range []State{StateProvisioning, StateUpdating, StateFailed, StateDeleting} {
			t.Run(kind+"/"+string(state), func(t *testing.T) {
				now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
				store, registry, provider, database := retirementFixture(t, kind, now.Add(-3*time.Hour))
				// Exercise catalog states independently of the lifecycle worker.
				switch s := store.(type) {
				case *MemoryStore:
					s.mu.Lock()
					d := s.databases[database.ID]
					d.State = state
					s.databases[database.ID] = d
					s.mu.Unlock()
				case *PostgresStore:
					if _, err := s.pool.Exec(context.Background(), "UPDATE managed_postgres_databases SET state = $1 WHERE id = $2", string(state), database.ID); err != nil {
						t.Fatal(err)
					}
				}
				if err := registry.UsagePolicy().Admit(context.Background(), store, database.AccountID, now); !errors.Is(err, ErrUsageStale) {
					t.Fatalf("unmetered %s resource admitted: %v", state, err)
				}
				collector, _ := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
				if _, err := collector.Collect(context.Background()); err != nil {
					t.Fatal(err)
				}
				snapshot, err := store.UsageSnapshot(context.Background(), database.AccountID, now)
				if err != nil || snapshot.ReadyDatabases != 0 || snapshot.ComputeUnitSeconds != 180 || snapshot.Stale(registry.UsagePolicy(), now) || len(provider.windows) != 3 {
					t.Fatalf("known resource escaped accounting: %+v, %v; windows=%v", snapshot, err, provider.windows)
				}
			})
		}
	}
}

func TestUsageDeletionCannotClearMissingHistory(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		for _, finish := range []bool{false, true} {
			name := "deleting"
			if finish {
				name = "deleted"
			}
			t.Run(kind+"/"+name, func(t *testing.T) {
				now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
				store, registry, provider, database := retirementFixture(t, kind, now.Add(-3*time.Hour))
				retirementDelete(t, store, database, now, finish)
				now = now.Add(time.Hour)
				provider.usageError = func(UsageWindow) error { return ErrUnavailable }
				collector, _ := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
				summary, err := collector.Collect(context.Background())
				if !errors.Is(err, ErrUnavailable) || summary.Discovered != 1 || summary.Deferred != 1 || len(provider.windows) != 1 {
					t.Fatalf("missing history was dropped: %+v, %v; windows=%v", summary, err, provider.windows)
				}
				if err := registry.UsagePolicy().Admit(context.Background(), store, database.AccountID, now); !errors.Is(err, ErrUsageStale) {
					t.Fatalf("shutdown cleared missing usage: %v", err)
				}
				// Moving into a new billing period does not forgive an old gap.
				if err := registry.UsagePolicy().Admit(context.Background(), store, database.AccountID, now.AddDate(0, 1, 0)); !errors.Is(err, ErrUsageStale) {
					t.Fatalf("month rollover cleared missing usage: %v", err)
				}
			})
		}
	}
}

func TestUsageRetirementRecoversAndCorrectsFiniteWindowsAcrossRestart(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 4, 13, 17, 0, 0, time.UTC)
			store, registry, provider, database := retirementFixture(t, kind, now.Add(-3*time.Hour))
			deleted := retirementDelete(t, store, database, now, true)
			end := deleted.DeletedAt.UTC().Truncate(time.Hour).Add(time.Hour)
			collect := func() error {
				collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
				if err != nil {
					t.Fatal(err)
				}
				_, err = collector.Collect(ctx)
				return err
			}
			// Shutdown in the middle of an hour must wait for that hour to close.
			if err := collect(); !errors.Is(err, ErrUsageStale) || len(provider.windows) != 0 {
				t.Fatalf("requested open final window: %v; windows=%v", err, provider.windows)
			}
			now = end
			if err := collect(); err != nil || len(provider.windows) != 4 {
				t.Fatalf("final recovery: %v; windows=%v", err, provider.windows)
			}
			if err := registry.UsagePolicy().Admit(ctx, store, database.AccountID, now); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("final corrections not yet observed: %v", err)
			}
			now = end.Add(3 * time.Hour)
			missing := end.Add(-2 * time.Hour)
			provider.usageError = func(window UsageWindow) error {
				if window.From.Equal(missing) {
					return ErrUnavailable
				}
				return nil
			}
			provider.usageReadings = func(UsageWindow) []MeterReading { return []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 9}} }
			if err := collect(); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("final correction failure: %v", err)
			}
			if err := registry.UsagePolicy().Admit(ctx, store, database.AccountID, now); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("one successful correction hid another failed window: %v", err)
			}
			provider.usageError = nil
			now = now.Add(time.Minute)
			if err := collect(); err != nil {
				t.Fatal(err)
			}
			if err := registry.UsagePolicy().Admit(ctx, store, database.AccountID, now); err != nil {
				t.Fatalf("finite accounting could not complete: %v", err)
			}
			snapshot, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil || snapshot.ComputeUnitSeconds != 87 || snapshot.ReadyDatabases != 0 {
				t.Fatalf("final corrections were not replacements: %+v, %v", snapshot, err)
			}
			calls := len(provider.windows)
			now = now.AddDate(0, 1, 0)
			if err := collect(); err != nil || len(provider.windows) != calls {
				t.Fatalf("completed tombstone kept polling: %v; calls=%d -> %d", err, calls, len(provider.windows))
			}
			if err := registry.UsagePolicy().Admit(ctx, store, database.AccountID, now); err != nil {
				t.Fatalf("complete terminal coverage aged into staleness: %v", err)
			}
			for _, window := range provider.windows {
				if window.To.After(end) {
					t.Fatalf("requested post-shutdown consumption: %+v", window)
				}
			}
		})
	}
}

func TestUsageRetirementWritesRejectPostShutdownWindowsAndWrongOwners(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, _, _, database := retirementFixture(t, kind, now.Add(-time.Hour))
			deleted := retirementDelete(t, store, database, now, true)
			end := deleted.DeletedAt.UTC().Truncate(time.Hour).Add(time.Hour)
			valid := UsageRecord{AccountID: database.AccountID, DatabaseID: database.ID,
				BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
				WindowFrom: database.CreatedAt.Truncate(time.Hour), WindowTo: end.Add(-time.Hour),
				ObservedAt: end.Add(3 * time.Hour), Meter: MeterComputeUnitSeconds, Quantity: 7}
			if err := store.RecordUsage(ctx, []UsageRecord{valid}); err != nil {
				t.Fatalf("retained history cannot be imported: %v", err)
			}
			for _, mutate := range []func(*UsageRecord){
				func(r *UsageRecord) { r.WindowFrom, r.WindowTo = end, end.Add(time.Hour) },
				func(r *UsageRecord) { r.AccountID = uuid.NewString() },
				func(r *UsageRecord) {
					r.BackendFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
				},
				func(r *UsageRecord) { r.ObservedAt = r.WindowFrom },
			} {
				record := valid
				mutate(&record)
				if err := store.RecordUsage(ctx, []UsageRecord{record}); !errors.Is(err, ErrConflict) {
					t.Fatalf("invalid terminal write accepted: %+v, %v", record, err)
				}
			}
			snapshot, err := store.UsageSnapshot(ctx, database.AccountID, now)
			if err != nil || snapshot.ComputeUnitSeconds != 7 {
				t.Fatalf("rejected write changed ledger: %+v, %v", snapshot, err)
			}
		})
	}
}

func TestUsageRetiredRestoresKeepSharedRootAccounting(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		for _, deleteRoot := range []bool{false, true} {
			name := "active-root"
			if deleteRoot {
				name = "deleted-root"
			}
			t.Run(kind+"/"+name, func(t *testing.T) {
				ctx := context.Background()
				now := time.Date(2026, 10, 4, 13, 17, 0, 0, time.UTC)
				store, registry, provider, root := retirementFixture(t, kind, now.Add(-3*time.Hour))
				backend := registry.backends[root.BackendID]
				backend.Capabilities.RestoreUsageIncludedInSource = true
				backend.Capabilities.RestoreUsageIsolated = false
				registry.backends[root.BackendID] = backend
				input := postgresTestDatabase(root.AccountID, "restored", now.Add(-2*time.Hour))
				input.BackendID, input.BackendFingerprint = root.BackendID, root.BackendFingerprint
				input.RestoreSourceDatabaseID, input.RestoreSourceResourceID = root.ID, root.ProviderResourceID
				input.RestorePointInTime = input.CreatedAt.Add(-time.Minute)
				child := retirementReadyDatabase(t, store, input)
				retirementDelete(t, store, child, now, true)
				if deleteRoot {
					retirementDelete(t, store, root, now.Add(time.Minute), true)
					now = time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC)
				}
				// A deleted child of a live root inherits the active aggregate's
				// freshness immediately, including inside its shutdown window.
				collector, _ := NewUsageCollector(registry, store, UsageCollectorOptions{BatchSize: 1, Now: func() time.Time { return now }})
				summary, err := collector.Collect(ctx)
				if err != nil || summary.Discovered != 2 || summary.IncludedInSourceUsage != 1 {
					t.Fatalf("retired restore lost root coverage: %+v, %v", summary, err)
				}
				want := int64(3 * 60)
				if deleteRoot {
					want = 4 * 60
				}
				snapshot, err := store.UsageSnapshot(ctx, root.AccountID, now)
				if err != nil || snapshot.ComputeUnitSeconds != want || snapshot.Stale(registry.UsagePolicy(), now) {
					t.Fatalf("shared aggregate double-counted or remained stale: %+v, %v", snapshot, err)
				}
				for _, resourceID := range provider.usageCalls {
					if resourceID != root.ProviderResourceID {
						t.Fatalf("restore descendant fetched its own aggregate: %q", resourceID)
					}
				}
			})
		}
	}
}

func TestUsageRetirementBacklogResumesWithinBudgetAcrossBillingMonths(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			deletedAt := time.Date(2026, 10, 1, 0, 17, 0, 0, time.UTC)
			store, registry, provider, database := retirementFixture(t, kind, deletedAt.Add(-49*time.Hour))
			retirementDelete(t, store, database, deletedAt, true)
			now := deletedAt.Add(24 * time.Hour)
			for sweep := 0; sweep < 3; sweep++ {
				calls := len(provider.windows)
				collector, _ := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
				_, err := collector.Collect(ctx)
				if sweep < 2 && !errors.Is(err, ErrUsageStale) || sweep == 2 && err != nil {
					t.Fatalf("sweep %d: %v", sweep, err)
				}
				if len(provider.windows)-calls > maximumUsageWindowsPerSweep {
					t.Fatalf("sweep %d exceeded request budget", sweep)
				}
				if err := registry.UsagePolicy().Admit(ctx, store, database.AccountID, now); sweep < 2 && !errors.Is(err, ErrUsageStale) || sweep == 2 && err != nil {
					t.Fatalf("sweep %d admission: %v", sweep, err)
				}
			}
			for _, period := range []struct {
				at       time.Time
				quantity int64
			}{{now, 60}, {now.AddDate(0, -1, 0), 49 * 60}} {
				snapshot, err := store.UsageSnapshot(ctx, database.AccountID, period.at)
				if err != nil || snapshot.ComputeUnitSeconds != period.quantity {
					t.Fatalf("month %s lost terminal history: %+v, %v", period.at, snapshot, err)
				}
			}
		})
	}
}

type retirementChangingCatalogStore struct {
	UsageStore
	memory *MemoryStore
	calls  int
}

func (s *retirementChangingCatalogStore) ListUsageDatabases(ctx context.Context, after UsageDatabaseCursor, limit int) ([]Database, error) {
	rows, err := s.UsageStore.ListUsageDatabases(ctx, after, limit)
	s.calls++
	if s.calls == 1 && len(rows) > 0 {
		s.memory.mu.Lock()
		database := s.memory.databases[rows[0].ID]
		database.State = StateDeleting
		database.UpdatedAt = database.UpdatedAt.Add(time.Second)
		s.memory.databases[database.ID] = database
		s.memory.mu.Unlock()
	}
	return rows, err
}

func TestUsageLifecycleCursorMovementDoesNotMultiplyRequestBudgets(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	memory, registry, provider := usageFleetFixture(t, now, 49*time.Hour, 2)
	store := &retirementChangingCatalogStore{UsageStore: memory, memory: memory}
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{BatchSize: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := collector.Collect(context.Background())
	if !errors.Is(err, ErrUsageStale) || summary.Discovered != 2 || summary.Deferred != 2 {
		t.Fatalf("moving row created duplicate work: %+v, %v", summary, err)
	}
	calls := map[string]int{}
	for _, resourceID := range provider.usageCalls {
		calls[resourceID]++
	}
	for resourceID, count := range calls {
		if count != maximumUsageWindowsPerSweep {
			t.Fatalf("resource %s received %d requests, want %d", resourceID, count, maximumUsageWindowsPerSweep)
		}
	}
}

func TestUsageEndUsesUTCAndDoesNotAddWindowAtExactBoundary(t *testing.T) {
	for _, hours := range []int{1, 2, 3, 4, 6, 8, 12, 24} {
		window := time.Duration(hours) * time.Hour
		boundary := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
		for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			at := boundary.Add(offset).In(time.FixedZone("offset", 3*3600))
			want := boundary
			if offset > 0 {
				want = want.Add(window)
			}
			if got := usageEnd(at, window); !got.Equal(want) || got.Location() != time.UTC {
				t.Fatalf("end(%v, %v)=%v, want %v", at, window, got, want)
			}
		}
	}
}
