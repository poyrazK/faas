package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func enabledUsagePolicy() UsagePolicy {
	return UsagePolicy{
		Enabled: true, CollectionInterval: 5 * time.Minute, Window: time.Hour,
		StaleAfter: 3 * time.Hour, MaxMonthlyCostMillicents: 100,
		MaxMonthlyComputeUnitSeconds: 1000, MaxMonthlyStorageByteSeconds: 1 << 50,
		MaxMonthlyHistoryByteSeconds: 1 << 50,
		MaxMonthlyEgressBytes:        1 << 20,
		ComputeUnitHourMillicents:    3600, StorageGiBHourMillicents: 2500,
		HistoryGiBHourMillicents: 500, EgressGiBMillicents: 1000,
	}
}

func TestUsagePolicyCostRoundsUpWithoutOverflow(t *testing.T) {
	policy := enabledUsagePolicy()
	cost, err := policy.Cost(MeterReading{Meter: MeterComputeUnitSeconds, Quantity: 1})
	if err != nil || cost != 1 {
		t.Fatalf("one compute second cost = %d, %v", cost, err)
	}
	cost, err = policy.Cost(MeterReading{Meter: MeterEgressBytes, Quantity: 1})
	if err != nil || cost != 1 {
		t.Fatalf("one egress byte cost = %d, %v", cost, err)
	}
	cost, err = policy.Cost(MeterReading{Meter: MeterStorageByteSeconds, Quantity: bytesPerGiB * secondsPerHour})
	if err != nil || cost != 2500 {
		t.Fatalf("one storage GiB-hour cost = %d, %v", cost, err)
	}
	cost, err = policy.Cost(MeterReading{Meter: MeterHistoryByteSeconds, Quantity: bytesPerGiB * secondsPerHour})
	if err != nil || cost != 500 {
		t.Fatalf("one history GiB-hour cost = %d, %v", cost, err)
	}
	if _, err := policy.Cost(MeterReading{Meter: MeterComputeUnitSeconds, Quantity: 1 << 62}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("overflow cost = %v, want ErrUnavailable", err)
	}
}

func TestUsageSnapshotExceedsStorageAndHistoryCaps(t *testing.T) {
	policy := enabledUsagePolicy()
	snapshot := UsageSnapshot{CostMillicents: 1, StorageByteSeconds: policy.MaxMonthlyStorageByteSeconds}
	if !snapshot.Exceeds(policy) {
		t.Fatal("storage cap was not enforced")
	}
	snapshot = UsageSnapshot{CostMillicents: 1, HistoryByteSeconds: policy.MaxMonthlyHistoryByteSeconds}
	if !snapshot.Exceeds(policy) {
		t.Fatal("history cap was not enforced")
	}
}

func TestMemoryUsageLedgerIsIdempotentAndAdmitsFreshAccounts(t *testing.T) {
	store := NewMemoryStore()
	backend := "primary-a"
	fingerprint := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	database := Database{
		ID: "db-1", AccountID: "account-1", Name: "orders", BackendID: backend,
		BackendFingerprint: fingerprint, State: StateReady, ProviderResourceID: "provider-1",
		CreatedAt: now, UpdatedAt: now,
	}
	store.databases[database.ID] = database
	windowFrom := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	record := UsageRecord{
		AccountID: database.AccountID, DatabaseID: database.ID, BackendID: backend,
		BackendFingerprint: fingerprint, WindowFrom: windowFrom, WindowTo: windowFrom.Add(time.Hour),
		ObservedAt: now, Meter: MeterComputeUnitSeconds, Quantity: 10, CostMillicents: 10,
	}
	if err := store.RecordUsage(context.Background(), []UsageRecord{record}); err != nil {
		t.Fatal(err)
	}
	record.Quantity = 20
	if err := store.RecordUsage(context.Background(), []UsageRecord{record}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.UsageSnapshot(context.Background(), database.AccountID, now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReadyDatabases != 1 || snapshot.ComputeUnitSeconds != 20 || snapshot.CostMillicents != 10 || !snapshot.LastObservedAt.Equal(now) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	policy := enabledUsagePolicy()
	if err := policy.Admit(context.Background(), store, database.AccountID, now); err != nil {
		t.Fatalf("fresh account rejected: %v", err)
	}
	if err := policy.Admit(context.Background(), store, database.AccountID, now.Add(4*time.Hour)); !errors.Is(err, ErrUsageStale) {
		t.Fatalf("stale account = %v, want ErrUsageStale", err)
	}
}

func TestServiceAdmissionRunsOnlyForNewReservations(t *testing.T) {
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	registry := testRegistry(t, provider, nil)
	store := NewMemoryStore()
	service, err := NewService(registry, store, ServiceOptions{
		ProvisioningEnabled: func() bool { return true },
		Admit:               func(context.Context, string) error { return ErrUsageStale },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()}
	if _, err := service.Create(context.Background(), request); !errors.Is(err, ErrUsageStale) {
		t.Fatalf("new reservation = %v, want ErrUsageStale", err)
	}
	if provider.provisionCalls != 0 {
		t.Fatalf("provider called after admission rejection: %d", provider.provisionCalls)
	}
	service.admit = nil
	created, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	service.admit = func(context.Context, string) error { return ErrUsageStale }
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatalf("idempotent ready create = %v", err)
	}
	if created.State != StateReady {
		t.Fatalf("created = %+v", created)
	}
}

type usageTestProvider struct {
	*fakeProvider
	readings []MeterReading
}

func (p *usageTestProvider) Usage(_ context.Context, _ string, window UsageWindow) (Usage, error) {
	return Usage{Window: window, Readings: p.readings}, nil
}

func TestUsageCollectorRecordsCompleteProviderWindows(t *testing.T) {
	provider := &usageTestProvider{
		fakeProvider: &fakeProvider{capabilities: testCapabilities()},
		readings: []MeterReading{
			{Meter: MeterComputeUnitSeconds, Quantity: 3600},
			{Meter: MeterStorageByteSeconds, Quantity: bytesPerGiB * secondsPerHour},
			{Meter: MeterHistoryByteSeconds, Quantity: bytesPerGiB * secondsPerHour},
			{Meter: MeterEgressBytes, Quantity: 1 << 30},
		},
	}
	now := time.Date(2026, 9, 6, 12, 17, 0, 0, time.UTC)
	registry := testRegistry(t, provider, func(config *Config) {
		config.Usage = UsageConfig{
			Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
			StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 100000,
			MaxMonthlyComputeUnitSeconds: 100000, MaxMonthlyStorageByteSeconds: 1 << 50,
			MaxMonthlyHistoryByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 40,
			ComputeUnitHourMillicents: 3600, StorageGiBHourMillicents: 2500,
			HistoryGiBHourMillicents: 500, EgressGiBMillicents: 1000,
		}
	})
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	store.databases["db-1"] = Database{
		ID: "db-1", AccountID: "account-1", Name: "orders", Spec: testSpec(),
		BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		ProviderResourceID: "provider-1", State: StateReady, UpdatedAt: now,
	}
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := collector.Collect(context.Background())
	if err != nil || summary.Recorded != 1 || summary.Deferred != 0 {
		t.Fatalf("collection = %+v, %v", summary, err)
	}
	snapshot, err := store.UsageSnapshot(context.Background(), "account-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ComputeUnitSeconds != 3600 || snapshot.StorageByteSeconds != bytesPerGiB*secondsPerHour || snapshot.HistoryByteSeconds != bytesPerGiB*secondsPerHour || snapshot.EgressBytes != 1<<30 || snapshot.CostMillicents != 7600 {
		t.Fatalf("usage snapshot = %+v", snapshot)
	}
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatalf("idempotent collection: %v", err)
	}
	snapshot, err = store.UsageSnapshot(context.Background(), "account-1", now)
	if err != nil || snapshot.CostMillicents != 7600 {
		t.Fatalf("repeated snapshot = %+v, %v", snapshot, err)
	}
}
