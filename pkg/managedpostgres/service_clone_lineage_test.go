// adr: 590
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cloneLineageProvider struct {
	fakeProvider
	lineage *RestoreLineage
	edit    func(*ObservedDatabase)
}

func (p *cloneLineageProvider) Restore(ctx context.Context, request RestoreRequest) (ObservedDatabase, error) {
	observed, err := p.fakeProvider.Restore(ctx, request)
	if err == nil {
		p.lineage = &RestoreLineage{SourceResourceID: request.SourceResourceID, PointInTime: request.PointInTime}
		lineage := *p.lineage
		observed.RestoreLineage = &lineage
		observed.DataResourceID = observed.ProviderResourceID
		if p.edit != nil {
			p.edit(&observed)
		}
	}
	return observed, err
}

func (p *cloneLineageProvider) Inspect(ctx context.Context, id string) (ObservedDatabase, error) {
	observed, err := p.fakeProvider.Inspect(ctx, id)
	if err == nil && p.lineage != nil {
		lineage := *p.lineage
		observed.RestoreLineage = &lineage
		observed.DataResourceID = observed.ProviderResourceID
		if p.edit != nil {
			p.edit(&observed)
		}
	}
	return observed, err
}

func TestCloneRestoreRequiresProviderLineageBeforeAdoptionAndReadiness(t *testing.T) {
	faults := []struct {
		name string
		edit func(*ObservedDatabase)
		want error
	}{
		{"valid", func(*ObservedDatabase) {}, nil},
		{"missing_proof", func(o *ObservedDatabase) { o.RestoreLineage = nil }, ErrUnavailable},
		{"missing_data_identity", func(o *ObservedDatabase) { o.DataResourceID = "" }, ErrUnavailable},
		{"shared_data_identity", func(o *ObservedDatabase) { o.DataResourceID = o.RestoreLineage.SourceResourceID }, ErrConflict},
		{"missing_source", func(o *ObservedDatabase) { o.RestoreLineage.SourceResourceID = "" }, ErrUnavailable},
		{"missing_point", func(o *ObservedDatabase) { o.RestoreLineage.PointInTime = time.Time{} }, ErrUnavailable},
		{"wrong_source", func(o *ObservedDatabase) { o.RestoreLineage.SourceResourceID += "/another-branch" }, ErrConflict},
		{"wrong_point", func(o *ObservedDatabase) {
			o.RestoreLineage.PointInTime = o.RestoreLineage.PointInTime.Add(time.Microsecond)
		}, ErrConflict},
		{"shared_identity", func(o *ObservedDatabase) { o.ProviderResourceID = o.RestoreLineage.SourceResourceID }, ErrConflict},
	}
	for _, phase := range []string{"restore", "inspect"} {
		for _, fault := range faults {
			t.Run(phase+"/"+fault.name, func(t *testing.T) {
				ctx := t.Context()
				provider := &cloneLineageProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusReady}}
				store := NewMemoryStore()
				service := testService(t, testRegistry(t, provider, nil), store)
				source, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "source", Spec: testSpec()})
				if err != nil {
					t.Fatal(err)
				}
				now := service.now()
				target, _, err := store.Reserve(ctx, Database{ID: "target", AccountID: source.AccountID, Name: "stage-db", Spec: source.Spec,
					BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, RestoreSourceDatabaseID: source.ID,
					RestoreSourceResourceID: source.ProviderResourceID, RestorePointInTime: now.Add(-time.Hour), State: StateProvisioning,
					DesiredGeneration: 1, CreatedAt: now, UpdatedAt: now}, 2)
				if err != nil {
					t.Fatal(err)
				}
				// Model the private leased reservation writer. The ordinary
				// MemoryStore reservation API intentionally cannot assign this owner.
				store.mu.Lock()
				target.EnvironmentCloneOperationID = "clone-operation"
				store.databases[target.ID] = target
				store.mu.Unlock()
				if phase == "inspect" {
					provider.provisionStatus = ProviderStatusPending
					pending, err := service.Reconcile(ctx, source.AccountID, target.ID)
					if err != nil || pending.State != StateProvisioning || pending.ProviderResourceID == "" {
						t.Fatalf("verified pending restore = %+v, %v", pending, err)
					}
					service.now = func() time.Time { return now.Add(time.Minute) }
				}
				provider.edit = fault.edit
				result, err := service.Reconcile(ctx, source.AccountID, target.ID)
				want := fault.want
				if phase == "inspect" && fault.name == "shared_identity" {
					// Inspection also fences the already persisted target identity.
					want = ErrUnavailable
				}
				if !errors.Is(err, want) {
					t.Fatalf("reconcile = %v, want %v", err, want)
				}
				actual, getErr := store.Get(ctx, source.AccountID, target.ID)
				if getErr != nil {
					t.Fatal(getErr)
				}
				if err == nil {
					if result.State != StateReady || actual.ObservedGeneration != 1 {
						t.Fatalf("verified restore did not become ready: %+v", actual)
					}
				} else {
					if actual.State != StateProvisioning || actual.ObservedGeneration != 0 || actual.LeaseToken != "" || !actual.RetryAt.After(service.now()) {
						t.Fatalf("failed proof advanced readiness or retained lease: %+v", actual)
					}
					if phase == "restore" && actual.ProviderResourceID != "" {
						t.Fatal("failed proof persisted an adoptable provider identity")
					}
					if actual.LastErrorCode == "" {
						t.Fatal("failed proof has no stable retry reason")
					}
				}
				if provider.restoreCalls != 1 || phase == "inspect" && provider.inspectCalls != 1 {
					t.Fatalf("restore/inspect calls = %d/%d", provider.restoreCalls, provider.inspectCalls)
				}
				unchanged, _ := store.Get(ctx, source.AccountID, source.ID)
				if unchanged != source {
					// Database has only comparable fields; the source must be untouched.
					t.Fatal("target verification changed the source database")
				}
			})
		}
	}
}

type storeWithoutCloneRestoreProofs struct{ Store }

func TestCloneRestoreRejectsStoreWithoutAtomicReceiptCapability(t *testing.T) {
	for _, status := range []State{StateProvisioning, StateReady} {
		t.Run(string(status), func(t *testing.T) {
			provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
			store := NewMemoryStore()
			service := testService(t, testRegistry(t, provider, nil), storeWithoutCloneRestoreProofs{Store: store})
			source, err := service.Create(t.Context(), CreateRequest{AccountID: "account-a", Name: "source", Spec: testSpec()})
			if err != nil {
				t.Fatal(err)
			}
			target := source
			target.ID, target.Name, target.ProviderResourceID = "target", "stage-db", "isolated-target"
			target.State, target.EnvironmentCloneOperationID = status, "clone-operation"
			target.RestoreSourceDatabaseID, target.RestoreSourceResourceID = source.ID, source.ProviderResourceID
			target.RestorePointInTime = source.CreatedAt
			store.mu.Lock()
			store.databases[target.ID] = target
			store.mu.Unlock()
			if _, err := service.Reconcile(t.Context(), source.AccountID, target.ID); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("store without atomic receipt reconciled clone: %v", err)
			}
			if provider.restoreCalls != 0 || provider.inspectCalls != 0 {
				t.Fatal("unsupported receipt store contacted provider")
			}
		})
	}
}
