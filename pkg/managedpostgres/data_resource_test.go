// adr: 569
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type splitDataProvider struct {
	bindingProvider
	defaultBranch string
}

func (p *splitDataProvider) Provision(ctx context.Context, request ProvisionRequest) (ObservedDatabase, error) {
	observed, err := p.fakeProvider.Provision(ctx, request)
	observed.DataResourceID = observed.ProviderResourceID + "/" + p.defaultBranch
	return observed, err
}

func (p *splitDataProvider) Restore(ctx context.Context, request RestoreRequest) (ObservedDatabase, error) {
	observed, err := p.fakeProvider.Restore(ctx, request)
	observed.DataResourceID = observed.ProviderResourceID + "/br-stage"
	return observed, err
}

type lostDataIdentityAckStore struct {
	*MemoryStore
	loseAck bool
}

func (s *lostDataIdentityAckStore) FinishProvisionWithDataResource(ctx context.Context, database Database, observed ObservedDatabase, now time.Time) (Database, error) {
	ready, err := s.MemoryStore.FinishProvisionWithDataResource(ctx, database, observed, now)
	if err == nil && s.loseAck {
		s.loseAck = false
		return Database{}, ErrUnavailable
	}
	return ready, err
}

func TestDataResourceIdentityPinsBindingsRestoresAndCleanup(t *testing.T) {
	for _, loseAck := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost_readiness_ack"}[loseAck], func(t *testing.T) {
			ctx := t.Context()
			provider := &splitDataProvider{bindingProvider: bindingProvider{fakeProvider: fakeProvider{
				capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, deleteDone: true}, material: bindingTestMaterial()}, defaultBranch: "br-production"}
			store := &lostDataIdentityAckStore{MemoryStore: NewMemoryStore(), loseAck: loseAck}
			registry := testRegistry(t, provider, nil)
			service := testService(t, registry, store)
			source, err := service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "source", Spec: testSpec()})
			if loseAck {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("lost acknowledgement = %v", err)
				}
				source, err = store.FindByName(ctx, "account-a", "source")
				if err == nil {
					source, err = service.Reconcile(ctx, source.AccountID, source.ID)
				}
			}
			if err != nil || source.State != StateReady || source.DataResourceID != source.ProviderResourceID+"/br-production" || provider.provisionCalls != 1 {
				t.Fatalf("ready source lost its observed dataset or repeated provisioning: %+v, %v", source, err)
			}
			provider.defaultBranch = "br-replacement"
			bindings, err := NewBindingService(registry, store, store, newBindingCredentialSink(), BindingServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: service.now})
			if err != nil {
				t.Fatal(err)
			}
			binding, err := bindings.Create(ctx, CreateBindingRequest{AccountID: source.AccountID, DatabaseID: source.ID, AppID: "app-a", Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite})
			if err != nil || provider.lastIssueRequest.ProviderResourceID != source.DataResourceID {
				t.Fatalf("credentials followed changed default: %v", err)
			}
			binding, err = bindings.Rotate(ctx, source.AccountID, binding.ID)
			if err != nil || provider.lastIssueRequest.ProviderResourceID != source.DataResourceID || binding.CredentialGeneration != 2 {
				t.Fatalf("rotation followed changed default: %v", err)
			}
			point := service.now().Add(-time.Hour)
			target, err := service.Restore(ctx, RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "stage", PointInTime: point})
			if err != nil || target.RestoreSourceResourceID != source.DataResourceID || provider.lastRestore.SourceResourceID != source.DataResourceID || target.DataResourceID == source.DataResourceID || target.DataResourceID == "" {
				t.Fatalf("restore followed changed default or shared data: %+v, %v", target, err)
			}
			if _, err := bindings.Delete(ctx, source.AccountID, binding.ID); err != nil || provider.lastRevokeRequest.ProviderResourceID != source.DataResourceID {
				t.Fatalf("revocation followed changed default: %v", err)
			}
			if _, err := service.Delete(ctx, target.AccountID, target.ID); err != nil || provider.lastDelete.ProviderResourceID != target.ProviderResourceID {
				t.Fatalf("stage cleanup used dataset alias: %v", err)
			}
			if _, err := service.Delete(ctx, source.AccountID, source.ID); err != nil || provider.lastDelete.ProviderResourceID != source.ProviderResourceID {
				t.Fatalf("root cleanup lost lifecycle ownership: %v", err)
			}
		})
	}
}

func TestMemoryDataResourceProvisionRejectsStaleEvidenceAtomically(t *testing.T) {
	for _, fault := range []string{"lease", "expired", "spec", "generation", "provider", "backend", "data_identity", "missing_identity", "not_ready", "legacy_ready"} {
		t.Run(fault, func(t *testing.T) {
			store := NewMemoryStore()
			now := time.Now().UTC()
			input := Database{ID: "database", AccountID: "account", Name: "orders", Spec: testSpec(), BackendID: "backend", BackendFingerprint: "fingerprint", State: StateProvisioning, DesiredGeneration: 1, CreatedAt: now, UpdatedAt: now}
			if _, _, err := store.Reserve(t.Context(), input, 2); err != nil {
				t.Fatal(err)
			}
			claimed, err := store.Claim(t.Context(), input.AccountID, input.ID, "lease", StateProvisioning, now, now.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.RecordProviderResource(t.Context(), input.ID, claimed.LeaseToken, "project", now); err != nil {
				t.Fatal(err)
			}
			claimed.ProviderResourceID = "project"
			observed := ObservedDatabase{ProviderResourceID: "project", DataResourceID: "project/branch", Status: ProviderStatusReady, Spec: input.Spec}
			store.mu.Lock()
			actual := store.databases[input.ID]
			switch fault {
			case "lease":
				actual.LeaseToken = "other-lease"
			case "expired":
				actual.LeaseUntil = now
			case "spec":
				actual.Spec.PostgresMajor++
			case "generation":
				actual.DesiredGeneration++
			case "provider":
				actual.ProviderResourceID = "other-project"
			case "backend":
				actual.BackendFingerprint = "other-backend"
			case "data_identity":
				actual.DataResourceID = "project/other-branch"
			case "missing_identity":
				observed.DataResourceID = ""
			case "not_ready":
				observed.Status = ProviderStatusPending
			case "legacy_ready":
				actual.State = StateReady
			}
			store.databases[input.ID] = actual
			store.mu.Unlock()
			if _, err := store.FinishProvisionWithDataResource(t.Context(), claimed, observed, now); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale evidence accepted: %v", err)
			}
			after, err := store.Get(t.Context(), input.AccountID, input.ID)
			if err != nil || after != actual {
				t.Fatalf("rejected pin modified catalog: %+v, %v", after, err)
			}
		})
	}
}
