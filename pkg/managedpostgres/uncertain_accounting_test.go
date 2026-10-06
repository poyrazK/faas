// adr: 581 — retain accounting obligations through uncertain provider mutations.

package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type uncertainAccountingProvider struct {
	*usageTestProvider
	store                retirementUsageStore
	account              string
	physical             bool
	intentBeforeCreate   bool
	identityBeforeDelete bool
	discoveryErr         error
	discoveryEmpty       bool
	discoveryHook        func()
	discoveryCalls       int
	deletionCalls        int
	lastDiscovery        ResourceDiscoveryRequest
	events               []string
}

func (p *uncertainAccountingProvider) Provision(ctx context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	database, err := p.store.Get(ctx, p.account, request.ResourceID)
	p.intentBeforeCreate = err == nil && database.AccountingRequired
	if !p.intentBeforeCreate {
		return ObservedDatabase{}, ErrConflict
	}
	p.physical = true
	return ObservedDatabase{}, ErrUnavailable // Accepted creation loses its response.
}

func (p *uncertainAccountingProvider) Restore(ctx context.Context, request RestoreRequest) (ObservedDatabase, error) {
	return p.Provision(ctx, ProvisionRequest{ResourceID: request.ResourceID, Spec: request.Spec})
}

func (p *uncertainAccountingProvider) Discover(_ context.Context, request ResourceDiscoveryRequest) (string, error) {
	p.discoveryCalls++
	p.events = append(p.events, "discover")
	p.lastDiscovery = request
	if p.discoveryHook != nil {
		p.discoveryHook()
	}
	if p.discoveryErr != nil {
		return "", p.discoveryErr
	}
	if p.discoveryEmpty {
		return "", nil
	}
	if !p.physical {
		return "", ErrNotFound
	}
	return "recovered-" + request.ResourceID, nil
}

func (p *uncertainAccountingProvider) Usage(ctx context.Context, identity string, window UsageWindow) (Usage, error) {
	p.events = append(p.events, "usage")
	return p.usageTestProvider.Usage(ctx, identity, window)
}

func TestUsageUnresolvedLegacyTombstoneCannotBeSettledByLedgerOrDiscovery(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			database := uncertainAccountingCreate(t, store, provider, service)
			// Model a legacy deletion that did not preserve identity.
			now = now.Add(3 * time.Hour)
			database = retirementDelete(t, store, database, now, true)
			for from := database.CreatedAt.Truncate(time.Hour); from.Before(now.Truncate(time.Hour)); from = from.Add(time.Hour) {
				if err := store.RecordUsage(context.Background(), []UsageRecord{{AccountID: database.AccountID, DatabaseID: database.ID,
					BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
					WindowFrom: from, WindowTo: from.Add(time.Hour), ObservedAt: now,
					Meter: MeterComputeUnitSeconds, Quantity: 60}}); err != nil {
					t.Fatal(err)
				}
			}
			now = now.AddDate(0, 1, 0)
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.Collect(context.Background()); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("legacy tombstone settled: %v", err)
			}
			if provider.discoveryCalls != 0 || len(provider.usageCalls) != 0 {
				t.Fatalf("unconfirmed shutdown promoted through discovery: %+v", provider)
			}
			snapshot, err := store.UsageSnapshot(context.Background(), provider.account, now)
			if err != nil || len(snapshot.Databases) != 1 || !snapshot.Databases[0].Unresolved || !snapshot.Stale(registry.UsagePolicy(), now) {
				t.Fatalf("unknown tombstone disappeared at month rollover: %+v %v", snapshot, err)
			}
		})
	}
}

func TestDiscoveredIdentityFencesPlacementLeaseAndExistingIdentity(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, _, provider, service := uncertainAccountingFixture(t, kind, &now)
			database := uncertainAccountingCreate(t, store, provider, service)
			for _, change := range []func(*Database){func(d *Database) { d.AccountID = uuid.NewString() },
				func(d *Database) { d.BackendID = "wrong-backend" }, func(d *Database) { d.BackendFingerprint = "wrong-fingerprint" }} {
				wrong := database
				change(&wrong)
				if err := store.RecordDiscoveredResource(context.Background(), wrong, "recovered", now); !errors.Is(err, ErrConflict) {
					t.Fatalf("wrong ownership accepted: %v", err)
				}
			}
			claimed, err := store.Claim(context.Background(), provider.account, database.ID, uuid.NewString(), StateProvisioning, now.Add(time.Hour), now.Add(time.Hour+time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.RecordDiscoveredResource(context.Background(), database, "recovered", now.Add(time.Hour)); !errors.Is(err, ErrConflict) {
				t.Fatalf("active lease bypassed: %v", err)
			}
			if err := store.BeginAccounting(context.Background(), database.ID, "wrong-token", now.Add(time.Hour)); !errors.Is(err, ErrConflict) {
				t.Fatalf("wrong intent lease: %v", err)
			}
			if err := store.BeginAccounting(context.Background(), database.ID, claimed.LeaseToken, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
				t.Fatalf("expired intent lease: %v", err)
			}
			if err := store.RecordDiscoveredResource(context.Background(), database, "recovered", now.Add(2*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := store.RecordDiscoveredResource(context.Background(), database, "different", now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
				t.Fatalf("identity changed: %v", err)
			}
		})
	}
}

func TestIdentityRecoveryUsesFleetRoundsAndProviderRequestBudget(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
	store, registry, provider, _ := uncertainAccountingFixture(t, "memory", &now)
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	start := now.Add(-50 * time.Hour)
	for _, name := range []string{"unknown-a", "unknown-b"} {
		input := postgresTestDatabase(provider.account, name, start)
		input.BackendID, input.BackendFingerprint = backend.ID, backend.Fingerprint
		database, _, err := store.Reserve(context.Background(), input, 100)
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := store.Claim(context.Background(), provider.account, database.ID, uuid.NewString(), StateProvisioning, start, start.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.BeginAccounting(context.Background(), database.ID, claimed.LeaseToken, start); err != nil {
			t.Fatal(err)
		}
		if err := store.Release(context.Background(), database.ID, claimed.LeaseToken, StateProvisioning, "unavailable", start, start); err != nil {
			t.Fatal(err)
		}
	}
	input := postgresTestDatabase(provider.account, "known", start)
	input.BackendID, input.BackendFingerprint = backend.ID, backend.Fingerprint
	retirementReadyDatabase(t, store, input)
	provider.physical = true
	collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Collect(context.Background()); !errors.Is(err, ErrUsageStale) {
		// The deliberately oversized backlog cannot finish in one bounded sweep.
		t.Fatal(err)
	}
	if len(provider.events) < 3 || provider.events[0] != "discover" || provider.events[1] != "discover" || provider.events[2] != "usage" {
		t.Fatalf("discovery ran outside recovery rounds: %v", provider.events)
	}
	if len(provider.events) != 3*maximumUsageWindowsPerSweep || provider.discoveryCalls != 2 {
		t.Fatalf("provider budget: requests=%d lookups=%d", len(provider.events), provider.discoveryCalls)
	}
}

func (p *uncertainAccountingProvider) Delete(ctx context.Context, request DeleteRequest) (DeleteResult, error) {
	p.deletionCalls++
	database, err := p.store.Get(ctx, p.account, request.ResourceID)
	p.identityBeforeDelete = err == nil && database.AccountingRequired &&
		database.ProviderResourceID != "" && database.ProviderResourceID == request.ProviderResourceID
	if !p.identityBeforeDelete {
		return DeleteResult{}, ErrConflict
	}
	p.physical = false
	return DeleteResult{Done: true}, nil
}

func uncertainAccountingFixture(t *testing.T, kind string, at *time.Time) (retirementUsageStore, *Registry, *uncertainAccountingProvider, *Service) {
	t.Helper()
	_, registry, usage := usageRecoveryFixture(t, *at, time.Hour)
	var store retirementUsageStore = NewMemoryStore()
	account := uuid.NewString()
	if kind == "postgres" {
		store, _, _, account = postgresStoreFixture(t)
	}
	provider := &uncertainAccountingProvider{usageTestProvider: usage, store: store, account: account}
	for id, backend := range registry.backends {
		backend.Provider = provider
		registry.backends[id] = backend
	}
	service := uncertainAccountingService(t, registry, store, at)
	return store, registry, provider, service
}

func uncertainAccountingService(t *testing.T, registry *Registry, store Store, at *time.Time) *Service {
	t.Helper()
	service, err := NewService(registry, store, ServiceOptions{
		Now: func() time.Time { return *at }, ProvisioningEnabled: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func uncertainAccountingCreate(t *testing.T, store Store, provider *uncertainAccountingProvider, service *Service) Database {
	t.Helper()
	_, err := service.Create(context.Background(), CreateRequest{AccountID: provider.account, Name: "uncertain", Spec: testSpec()})
	if !errors.Is(err, ErrUnavailable) || !provider.physical || !provider.intentBeforeCreate {
		t.Fatalf("lost create: error=%v physical=%v intent=%v", err, provider.physical, provider.intentBeforeCreate)
	}
	database, err := store.FindByName(context.Background(), provider.account, "uncertain")
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestUncertainProvisionRetainsAccountingAndPersistsDeletionIdentity(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			database := uncertainAccountingCreate(t, store, provider, service)
			if !database.AccountingRequired || database.ProviderResourceID != "" {
				t.Fatalf("uncertain catalog: %+v", database)
			}
			if err := registry.UsagePolicy().Admit(context.Background(), store, provider.account, now); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("uncertain creation escaped admission: %v", err)
			}
			now = now.Add(3 * time.Hour)
			// Recreate the service to prove ownership is catalog state, not process state.
			service = uncertainAccountingService(t, registry, store, &now)
			deleted, err := service.Delete(context.Background(), provider.account, database.ID)
			if err != nil || deleted.State != StateDeleted || !deleted.AccountingRequired || deleted.ProviderResourceID == "" ||
				!provider.identityBeforeDelete || provider.physical || provider.discoveryCalls != 1 {
				t.Fatalf("deletion did not retain identity: %+v err=%v provider=%+v", deleted, err, provider)
			}
			if err := registry.UsagePolicy().Admit(context.Background(), store, provider.account, now); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("uncollected terminal resource escaped admission: %v", err)
			}
			now = now.Add(4 * time.Hour)
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = collector.Collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := registry.UsagePolicy().Admit(context.Background(), store, provider.account, now); err != nil {
				t.Fatalf("terminal coverage not recovered: %v", err)
			}
			snapshot, err := store.UsageSnapshot(context.Background(), provider.account, now)
			if err != nil || snapshot.ComputeUnitSeconds == 0 {
				t.Fatalf("usage lost: %+v %v", snapshot, err)
			}
		})
	}
}

func TestUncertainCreationAbsenceKeepsDeletionPending(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			database := uncertainAccountingCreate(t, store, provider, service)
			provider.discoveryErr = ErrNotFound
			now = now.Add(time.Hour)
			_, err := service.Delete(context.Background(), provider.account, database.ID)
			if !errors.Is(err, ErrUnavailable) || provider.deletionCalls != 0 {
				t.Fatalf("absence treated as shutdown: %v calls=%d", err, provider.deletionCalls)
			}
			current, err := store.Get(context.Background(), provider.account, database.ID)
			if err != nil || current.State != StateDeleting || current.DeletedAt != nil || !current.AccountingRequired {
				t.Fatalf("lost uncertain deletion: %+v %v", current, err)
			}
			if err := registry.UsagePolicy().Admit(context.Background(), store, provider.account, now); !errors.Is(err, ErrUsageStale) {
				t.Fatalf("absence inferred zero: %v", err)
			}
			provider.discoveryErr = nil
			now = now.Add(time.Hour)
			if _, err := service.Delete(context.Background(), provider.account, database.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type accountingFailureStore struct {
	retirementUsageStore
	beginErr  error
	recordErr error
}

func (s *accountingFailureStore) BeginAccounting(ctx context.Context, id, lease string, at time.Time) error {
	if s.beginErr != nil {
		return s.beginErr
	}
	return s.retirementUsageStore.BeginAccounting(ctx, id, lease, at)
}
func (s *accountingFailureStore) RecordProviderResource(ctx context.Context, id, lease, identity string, at time.Time) error {
	if s.recordErr != nil {
		return s.recordErr
	}
	return s.retirementUsageStore.RecordProviderResource(ctx, id, lease, identity, at)
}

func TestUncertainMutationStopsBeforeFailedAccountingPersistence(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, _ := uncertainAccountingFixture(t, kind, &now)
			failed := &accountingFailureStore{retirementUsageStore: store, beginErr: ErrUnavailable}
			service := uncertainAccountingService(t, registry, failed, &now)
			_, err := service.Create(context.Background(), CreateRequest{AccountID: provider.account, Name: "uncertain", Spec: testSpec()})
			if !errors.Is(err, ErrUnavailable) || provider.physical {
				t.Fatalf("creation reached provider without intent: %v physical=%v", err, provider.physical)
			}
			failed.beginErr = nil
			now = now.Add(time.Hour)
			database := uncertainAccountingCreate(t, store, provider, service)
			failed.recordErr = ErrUnavailable
			now = now.Add(time.Hour)
			_, err = service.Delete(context.Background(), provider.account, database.ID)
			if !errors.Is(err, ErrUnavailable) || provider.deletionCalls != 0 || !provider.physical {
				t.Fatalf("deletion before identity commit: %v calls=%d", err, provider.deletionCalls)
			}
			failed.recordErr = nil
			now = now.Add(time.Hour)
			if _, err := service.Delete(context.Background(), provider.account, database.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUncertainIdentityDiscoveryFencesAnExpiredDeletionLease(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			database := uncertainAccountingCreate(t, store, provider, service)
			now = now.Add(time.Hour)
			provider.discoveryHook = func() { now = now.Add(3 * time.Minute) }
			_, err := service.Delete(context.Background(), provider.account, database.ID)
			if !errors.Is(err, ErrConflict) || provider.deletionCalls != 0 {
				t.Fatalf("expired lease deleted provider: %v calls=%d", err, provider.deletionCalls)
			}
			provider.discoveryHook = nil
			service = uncertainAccountingService(t, registry, store, &now)
			if _, err := service.Delete(context.Background(), provider.account, database.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUsageDiscoveryRecoversUncertainIdentityWithoutMutation(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			database := uncertainAccountingCreate(t, store, provider, service)
			now = now.Add(3 * time.Hour)
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.Collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, err := store.Get(context.Background(), provider.account, database.ID)
			if err != nil || current.ProviderResourceID == "" || provider.discoveryCalls != 1 || provider.deletionCalls != 0 || !provider.physical {
				t.Fatalf("read-only recovery: %+v %v", current, err)
			}
			if err := registry.UsagePolicy().Admit(context.Background(), store, provider.account, now); err != nil {
				t.Fatalf("recovered coverage: %v", err)
			}
			if _, err := collector.Collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			if provider.discoveryCalls != 1 {
				t.Fatalf("known identity rediscovered %d times", provider.discoveryCalls)
			}
		})
	}
}

func TestUncertainRestoreRecoveryKeepsSharedRootAccounting(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Microsecond)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			provider.capabilities.RestoreUsageIncludedInSource = true
			for id, backend := range registry.backends {
				backend.Capabilities.RestoreUsageIncludedInSource = true
				registry.backends[id] = backend
			}
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			input := postgresTestDatabase(provider.account, "source", now.Add(-3*time.Hour))
			input.BackendID, input.BackendFingerprint = backend.ID, backend.Fingerprint
			source, _, err := store.Reserve(t.Context(), input, 100)
			if err != nil {
				t.Fatal(err)
			}
			// Data pin publication uses the server clock, so retain a live lease
			// while keeping the original accounting source lifetime in the past.
			claimed, err := store.Claim(t.Context(), source.AccountID, source.ID, uuid.NewString(), StateProvisioning, now, now.Add(10*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			id := "provider-" + source.ID
			if err := store.RecordProviderResource(t.Context(), source.ID, claimed.LeaseToken, id, input.CreatedAt.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			claimed.ProviderResourceID = id
			source, err = store.(DataResourceProvisionStore).FinishProvisionWithDataResource(t.Context(), claimed,
				ObservedDatabase{ProviderResourceID: id, DataResourceID: id, Spec: input.Spec, Status: ProviderStatusReady}, now)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Restore(context.Background(), RestoreDatabaseRequest{AccountID: provider.account, SourceDatabaseID: source.ID,
				Name: "uncertain-restore", PointInTime: now.Add(-time.Hour)})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("uncertain restore: %v", err)
			}
			child, err := store.FindByName(context.Background(), provider.account, "uncertain-restore")
			if err != nil || !child.AccountingRequired || child.ProviderResourceID != "" {
				t.Fatalf("restore intent: %+v %v", child, err)
			}
			now = now.Add(time.Hour)
			if _, err := service.Delete(context.Background(), provider.account, child.ID); err != nil {
				t.Fatal(err)
			}
			if provider.lastDiscovery.RestoreSourceResourceID != source.ProviderResourceID {
				t.Fatalf("lost restore parent: %+v", provider.lastDiscovery)
			}
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.Collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			progress, err := store.UsageProgress(context.Background(), provider.account, child.ID, time.Hour)
			if err != nil || progress.SourceDatabaseID != source.ID {
				t.Fatalf("shared accounting: %+v %v", progress, err)
			}
			if len(provider.usageCalls) != 4 {
				t.Fatalf("restore consumption counted independently: calls=%d", len(provider.usageCalls))
			}
			if err := registry.UsagePolicy().Admit(context.Background(), store, provider.account, now); err != nil {
				t.Fatalf("shared root coverage: %v", err)
			}
		})
	}
}

// Embedding only Provider deliberately hides the optional discovery capability.
type undiscoverableAccountingProvider struct{ Provider }

func TestUncertainDeletionRequiresDiscoveryCapabilityAndIdentity(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		for _, mode := range []string{"unsupported", "empty"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
				store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
				database := uncertainAccountingCreate(t, store, provider, service)
				want := ErrUnavailable
				if mode == "unsupported" {
					want = ErrUnsupported
					for id, backend := range registry.backends {
						backend.Provider = undiscoverableAccountingProvider{Provider: provider}
						registry.backends[id] = backend
					}
				} else {
					provider.discoveryEmpty = true
				}
				now = now.Add(time.Hour)
				if _, err := service.Delete(context.Background(), provider.account, database.ID); !errors.Is(err, want) {
					t.Fatalf("delete without identity: %v", err)
				}
				current, err := store.Get(context.Background(), provider.account, database.ID)
				if err != nil || current.State != StateDeleting || !current.AccountingRequired || current.ProviderResourceID != "" || current.DeletedAt != nil || provider.deletionCalls != 0 {
					t.Fatalf("unresolved deletion retired: %+v %v calls=%d", current, err, provider.deletionCalls)
				}
				if err := registry.UsagePolicy().Admit(context.Background(), store, provider.account, now); !errors.Is(err, ErrUsageStale) {
					t.Fatalf("unresolved accounting became fresh: %v", err)
				}
			})
		}
	}
}

func TestUsageDiscoveryCancellationRetainsUnresolvedObligation(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 4, 12, 17, 0, 0, time.UTC)
			store, registry, provider, service := uncertainAccountingFixture(t, kind, &now)
			database := uncertainAccountingCreate(t, store, provider, service)
			now = now.Add(3 * time.Hour)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider.discoveryHook = cancel
			collector, err := NewUsageCollector(registry, store, UsageCollectorOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collector.Collect(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			current, err := store.Get(context.Background(), provider.account, database.ID)
			if err != nil || !current.AccountingRequired || current.ProviderResourceID != "" || provider.discoveryCalls != 1 || len(provider.usageCalls) != 0 {
				t.Fatalf("canceled recovery mutated accounting: %+v %v calls=%d", current, err, provider.discoveryCalls)
			}
		})
	}
}
