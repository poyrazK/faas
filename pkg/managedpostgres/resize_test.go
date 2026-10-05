// adr: 593 — durable compute resizing, ambiguity recovery and generation fences.
package managedpostgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type resizeTestProvider struct {
	*fakeProvider
	applied      bool
	lostResponse bool
	pending      bool
	patches      int
	calls        int
	drift        bool
}

func (p *resizeTestProvider) Provision(ctx context.Context, r ProvisionRequest) (ObservedDatabase, error) {
	observed, err := p.fakeProvider.Provision(ctx, r)
	observed.DataResourceID = observed.ProviderResourceID + "/dataset"
	return observed, err
}

func (p *resizeTestProvider) Update(_ context.Context, r UpdateRequest) (ObservedDatabase, error) {
	p.calls++
	if !p.applied {
		p.patches++
		p.applied = true
	}
	if p.lostResponse {
		p.lostResponse = false
		return ObservedDatabase{}, ErrUnavailable
	}
	result := ObservedDatabase{ProviderResourceID: r.ResourceID, DataResourceID: r.DataResourceID, Spec: r.Spec, Status: ProviderStatusReady}
	if p.pending {
		result.Status = ProviderStatusPending
	}
	if p.drift {
		result.DataResourceID = "other-dataset"
	}
	return result, nil
}

func resizeFixture(t *testing.T) (*Service, *MemoryStore, *resizeTestProvider, Database, *time.Time) {
	t.Helper()
	capabilities := testCapabilities()
	capabilities.ClassResize = true
	provider := &resizeTestProvider{fakeProvider: &fakeProvider{capabilities: capabilities, provisionStatus: ProviderStatusReady}}
	store := NewMemoryStore()
	registry := testRegistry(t, provider, nil)
	now := time.Now().UTC()
	service, err := NewService(registry, store, ServiceOptions{Now: func() time.Time { return now }, ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	database, err := service.Create(t.Context(), CreateRequest{AccountID: "account", Name: "orders", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, provider, database, &now
}

func resizeRequest(d Database) ResizeDatabaseRequest {
	return ResizeDatabaseRequest{AccountID: d.AccountID, DatabaseID: d.ID, RequestID: uuid.NewString(), TargetClass: ClassBurstable}
}

func TestResizeRecoversLostResponseAfterRestartWithClosedAdmission(t *testing.T) {
	service, store, provider, database, now := resizeFixture(t)
	request := resizeRequest(database)
	operation, err := service.Resize(t.Context(), request)
	if err != nil || operation.State != ResizePending || provider.calls != 0 {
		t.Fatalf("intent: %+v %v calls=%d", operation, err, provider.calls)
	}
	pending, _ := store.Get(t.Context(), database.AccountID, database.ID)
	if pending.State != StateUpdating || pending.Spec != database.Spec || pending.DesiredGeneration != 2 || pending.ObservedGeneration != 1 {
		t.Fatalf("reservation replaced confirmed spec: %+v", pending)
	}
	provider.lostResponse = true
	if _, err := service.Reconcile(t.Context(), database.AccountID, database.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lost response", err)
	}
	operation, err = service.GetResize(t.Context(), database.AccountID, database.ID, request.RequestID)
	if err != nil || operation.State != ResizePending || operation.LastErrorCode != "unavailable" {
		t.Fatalf("uncertainty lost: %+v %v", operation, err)
	}
	*now = now.Add(time.Minute)
	restarted, err := NewService(service.registry, store, ServiceOptions{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(restarted, ReconcilerOptions{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := reconciler.Sweep(t.Context())
	if err != nil || summary.Completed != 1 || provider.patches != 1 {
		t.Fatalf("restart: %+v %v patches=%d", summary, err, provider.patches)
	}
	result, _ := store.Get(t.Context(), database.AccountID, database.ID)
	operation, err = restarted.Resize(t.Context(), request)
	if err != nil || operation.State != ResizeSucceeded || operation.CompletedAt.IsZero() || result.Spec.Class != ClassBurstable ||
		result.Spec.Region != database.Spec.Region || result.ProviderResourceID != database.ProviderResourceID || result.DataResourceID != database.DataResourceID ||
		result.ObservedGeneration != result.DesiredGeneration || !result.AccountingRequired {
		t.Fatalf("completion: %+v %+v %v", result, operation, err)
	}
	request.TargetClass = ClassDevelopment
	if _, err := restarted.Resize(t.Context(), request); !errors.Is(err, ErrConflict) {
		t.Fatal("UUID reused with different class", err)
	}
}

func TestResizeWaitsForProviderWorkAndRejectsDataDrift(t *testing.T) {
	service, store, provider, database, now := resizeFixture(t)
	request := resizeRequest(database)
	if _, err := service.Resize(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	provider.pending = true
	pending, err := service.Reconcile(t.Context(), database.AccountID, database.ID)
	if err != nil || pending.State != StateUpdating || pending.Spec.Class != ClassDevelopment {
		t.Fatal("early completion", pending, err)
	}
	*now = now.Add(time.Minute)
	provider.pending = false
	provider.drift = true
	if _, err := service.Reconcile(t.Context(), database.AccountID, database.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("adopted changed data identity", err)
	}
	pending, _ = store.Get(t.Context(), database.AccountID, database.ID)
	if pending.State != StateUpdating || pending.LastErrorCode != "resize_observation_mismatch" || pending.ObservedGeneration != 1 {
		t.Fatal("lost mismatch fence", pending)
	}
}

func TestResizeStaleLeaseAndGenerationCannotComplete(t *testing.T) {
	service, store, _, database, now := resizeFixture(t)
	operation, err := service.Resize(t.Context(), resizeRequest(database))
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Claim(t.Context(), database.AccountID, database.ID, "old-worker", StateUpdating, *now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	observed := ObservedDatabase{ProviderResourceID: database.ProviderResourceID, DataResourceID: database.DataResourceID, Spec: operation.TargetSpec(), Status: ProviderStatusReady}
	*now = now.Add(2 * time.Minute)
	if _, err := store.FinishResize(t.Context(), old, operation, observed, *now); !errors.Is(err, ErrConflict) {
		t.Fatal("expired lease completed", err)
	}
	replacement, err := store.Claim(t.Context(), database.AccountID, database.ID, "replacement", StateUpdating, *now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishResize(t.Context(), old, operation, observed, *now); !errors.Is(err, ErrConflict) {
		t.Fatal("old worker completed", err)
	}
	ready, err := store.FinishResize(t.Context(), replacement, operation, observed, *now)
	if err != nil {
		t.Fatal(err)
	}
	next := ResizeDatabaseRequest{AccountID: ready.AccountID, DatabaseID: ready.ID, RequestID: uuid.NewString(), TargetClass: ClassDevelopment}
	if _, err := service.Resize(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishResize(t.Context(), replacement, operation, observed, *now); !errors.Is(err, ErrConflict) {
		t.Fatal("old generation completed newer intent", err)
	}
}

func TestResizeAdmissionConflictsAndPreservesBindings(t *testing.T) {
	for _, kind := range []string{"binding creation", "rotation", "pending restore", "cutover", "provider unsupported", "missing data identity", "plan denied", "usage blocked", "clone owned"} {
		t.Run(kind, func(t *testing.T) {
			service, store, provider, database, now := resizeFixture(t)
			switch kind {
			case "binding creation":
				store.bindings["binding"] = Binding{DatabaseID: database.ID, State: BindingStateProvisioning}
			case "rotation":
				store.bindings["binding"] = Binding{DatabaseID: database.ID, State: BindingStateReady, RotationPreviousGeneration: 1}
			case "pending restore":
				store.databases["restore"] = Database{RestoreSourceDatabaseID: database.ID, State: StateProvisioning}
			case "cutover":
				store.cutovers["cutover"] = Cutover{State: CutoverPrepared, Source: database}
			case "provider unsupported":
				backend := service.registry.backends[database.BackendID]
				backend.Capabilities.ClassResize = false
				service.registry.backends[database.BackendID] = backend
			case "missing data identity":
				database.DataResourceID = ""
				store.databases[database.ID] = database
			case "plan denied":
				service.admitResize = func(context.Context, string, Spec) error { return ErrQuotaExceeded }
			case "usage blocked":
				service.admit = func(context.Context, string) error { return ErrUsageStale }
			case "clone owned":
				database.EnvironmentCloneOperationID = "clone"
				store.databases[database.ID] = database
			}
			if _, err := service.Resize(t.Context(), resizeRequest(database)); err == nil {
				t.Fatal("unsafe resize admitted")
			}
			current, _ := store.Get(t.Context(), database.AccountID, database.ID)
			if current.State != StateReady || provider.calls != 0 {
				t.Fatal("rejected intent changed state")
			}
			_ = now
		})
	}
	service, store, _, database, _ := resizeFixture(t)
	binding := Binding{ID: "binding", DatabaseID: database.ID, State: BindingStateReady, CredentialGeneration: 3, ProviderIdentityID: "role", CredentialRef: "sealed-reference"}
	store.bindings[binding.ID] = binding
	if _, err := service.Resize(t.Context(), resizeRequest(database)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(t.Context(), database.AccountID, database.ID); err != nil {
		t.Fatal(err)
	}
	if store.bindings[binding.ID] != binding {
		t.Fatal("resize rewrote credentials")
	}
}

func TestResizeAndDeletionReservationAreSerialized(t *testing.T) {
	for i := 0; i < 10; i++ {
		service, store, _, database, now := resizeFixture(t)
		operation := ResizeOperation{ID: uuid.NewString(), AccountID: database.AccountID, DatabaseID: database.ID, BackendID: database.BackendID,
			BackendFingerprint: database.BackendFingerprint, ProviderResourceID: database.ProviderResourceID, DataResourceID: database.DataResourceID,
			SourceSpec: database.Spec, TargetClass: ClassBurstable, Generation: 2, State: ResizePending, CreatedAt: *now}
		var resizeErr, deleteErr error
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			_, resizeErr = store.ReserveResize(context.Background(), database, operation, *now)
		}()
		go func() {
			defer wait.Done()
			_, deleteErr = store.ClaimDelete(context.Background(), database.AccountID, database.ID, "delete", *now, now.Add(time.Minute))
		}()
		wait.Wait()
		if (resizeErr == nil) == (deleteErr == nil) {
			t.Fatalf("both mutations accepted/rejected: %v %v", resizeErr, deleteErr)
		}
		_ = service
	}
}
