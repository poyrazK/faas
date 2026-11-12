package managedpostgres

import (
	"context"
	"testing"
	"time"
)

func TestStorageCapsIncludeRetainedRestoreSource(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(map[bool]string{false: "purged source kept for copies", true: "source in undo window"}[scheduled], func(t *testing.T) {
			now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
			provider := &storageCapTestProvider{
				capTestProvider: &capTestProvider{
					fakeProvider: &fakeProvider{capabilities: testCapabilities()},
					// No source entry: its disabled compute must never be observed.
					observed: map[string]ComputeCapObservation{"proj/br-copy": {Scope: "proj", PeriodStart: monthStart(now), UsedUnitSeconds: 10}},
					set:      map[string]int64{},
				},
				storage: map[string]StorageCapObservation{
					"proj":         {Scope: "proj", SizeBytes: 800 * mib, LimitBytes: 1000 * mib},
					"proj/br-copy": {Scope: "proj", SizeBytes: 200 * mib, LimitBytes: 1000 * mib},
				},
				storageSet: map[string]int64{},
			}
			registry := testRegistry(t, provider, nil)
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			store := NewMemoryStore()
			source := Database{ID: "a-source", AccountID: "account-a", State: StateDeleting, ProviderResourceID: "proj",
				BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Spec: testSpec(), UpdatedAt: now}
			source.Spec.StorageLimitBytes = 1000 * mib
			if scheduled {
				source.PurgeAfter = now.Add(7 * 24 * time.Hour)
			}
			copy := source
			copy.ID, copy.State, copy.ProviderResourceID = "b-copy", StateReady, "proj/br-copy"
			copy.RestoreSourceDatabaseID, copy.PurgeAfter = source.ID, time.Time{}
			store.databases[source.ID], store.databases[copy.ID] = source, copy
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Mode: CapEnforcementShadow, StorageMode: CapEnforcementOn, BatchSize: 1, Now: func() time.Time { return now },
				StorageQualification: testStorageCapQualification(backend),
				Ceiling: func(context.Context, string) (CapLimits, error) {
					return CapLimits{Ceiling: 1000, StoragePool: 1000 * mib}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := enforcer.Enforce(context.Background())
			if err != nil || summary.Failed != 0 || summary.StorageFailed != 0 || summary.StorageBlockedAccounts != 1 || provider.storageSet["proj"] != 200*mib {
				t.Fatalf("retained storage did not fill the pool: limits=%v summary=%+v err=%v", provider.storageSet, summary, err)
			}
			// Once the last copy is deleted, the ordinary delete waiver applies.
			copy.State = StateDeleted
			store.databases[copy.ID] = copy
			grouped, err := enforcer.liveDatabasesBySubject(context.Background())
			if err != nil || len(grouped) != 0 {
				t.Fatalf("source without copies kept counting: %+v, %v", grouped, err)
			}
		})
	}
}
