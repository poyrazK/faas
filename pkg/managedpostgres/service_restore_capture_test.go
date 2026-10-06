package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ADR-590: a restore uses the frozen source spec while preserving its physical
// source identity. Later desired source edits cannot change the stage copy.
func TestRestoreUsesCapturedSourceDefinition(t *testing.T) {
	ctx := context.Background()
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	store := NewMemoryStore()
	service := testService(t, testRegistry(t, provider, nil), store)
	source, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "source", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	definition := RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID}
	store.mu.Lock()
	edited := store.databases[source.ID]
	edited.Spec.Class, edited.Spec.StorageLimitBytes = ClassBurstable, 20<<30
	store.databases[source.ID] = edited
	store.mu.Unlock()
	request := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "captured", PointInTime: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), SourceDefinition: &definition}
	target, created, err := service.RestoreWithResult(ctx, request)
	if err != nil || !created || target.Spec != definition.Spec || provider.lastRestore.Spec != definition.Spec || provider.lastRestore.SourceResourceID != definition.ProviderResourceID {
		t.Fatalf("restore did not use captured spec/identity: created=%v err=%v", created, err)
	}
	again, created, err := service.RestoreWithResult(ctx, request)
	if err != nil || created || again.ID != target.ID || provider.restoreCalls != 1 {
		t.Fatalf("captured restore retry changed identity: created=%v err=%v calls=%d", created, err, provider.restoreCalls)
	}
	live, err := service.Get(ctx, source.AccountID, source.ID)
	if err != nil || live.Spec != edited.Spec {
		t.Fatal("restore changed the source desired spec")
	}
}

// ADR-590: captured restoration rejects physical-identity changes, expired
// recovery windows and conflicting adopted targets before provider I/O.
func TestRestoreCapturedDefinitionRejectsDriftBeforeProviderIO(t *testing.T) {
	for _, fault := range []string{"provider_identity", "backend_identity", "fingerprint", "invalid_spec", "shortened_retention", "target_spec", "target_origin"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
			store := NewMemoryStore()
			service := testService(t, testRegistry(t, provider, nil), store)
			source, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "source", Spec: testSpec()})
			if err != nil {
				t.Fatal(err)
			}
			definition := RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID}
			request := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "captured", PointInTime: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), SourceDefinition: &definition}
			var target Database
			if fault == "target_spec" || fault == "target_origin" {
				target, _, err = service.RestoreWithResult(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
			}
			store.mu.Lock()
			edited := store.databases[source.ID]
			switch fault {
			case "provider_identity":
				edited.ProviderResourceID = "replaced"
			case "backend_identity":
				edited.BackendID = "replaced"
			case "fingerprint":
				edited.BackendFingerprint = "replaced"
			case "invalid_spec":
				definition.Spec.PostgresMajor = 0
			case "shortened_retention":
				edited.Spec.RestoreWindowSeconds = 60
			case "target_spec":
				target.Spec.Class = ClassBurstable
				store.databases[target.ID] = target
			case "target_origin":
				target.RestoreSourceResourceID = "replaced"
				store.databases[target.ID] = target
			}
			store.databases[source.ID] = edited
			store.mu.Unlock()
			beforeCalls := provider.restoreCalls
			_, created, err := service.RestoreWithResult(ctx, request)
			if (!errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid)) || created || provider.restoreCalls != beforeCalls {
				t.Fatalf("drift accepted or provider contacted: created=%v err=%v calls=%d/%d", created, err, provider.restoreCalls, beforeCalls)
			}
			if target.ID == "" {
				if _, err := store.FindByName(ctx, source.AccountID, request.Name); !errors.Is(err, ErrNotFound) {
					t.Fatal("rejected restore left a target reservation")
				}
			}
		})
	}
}
