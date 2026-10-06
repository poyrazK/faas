// adr: 624 — durable idle policy changes share resize and lifecycle fences.
package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func policyFixture(t *testing.T) (*Service, *MemoryStore, *resizeTestProvider, Database, *time.Time) {
	t.Helper()
	service, store, provider, database, now := resizeFixture(t)
	provider.capabilities.ScaleToZeroUpdate = true
	backend, _ := service.registry.Resolve(database.BackendID, database.BackendFingerprint)
	backend.Capabilities.ScaleToZeroUpdate = true
	service.registry.backends[backend.ID] = backend
	return service, store, provider, database, now
}

func TestComputePolicyRecoveryReplayAndClassIsolation(t *testing.T) {
	service, store, provider, database, now := policyFixture(t)
	request := ChangeComputePolicyRequest{AccountID: database.AccountID, DatabaseID: database.ID, RequestID: uuid.NewString(), ScaleToZero: !database.Spec.ScaleToZero}
	operation, err := service.ChangeComputePolicy(t.Context(), request)
	if err != nil || !operation.PolicyChange || operation.State != ResizePending || provider.calls != 0 {
		t.Fatal(operation, err)
	}
	saved, _ := store.Get(t.Context(), database.AccountID, database.ID)
	if saved.Spec != database.Spec {
		t.Fatal("unconfirmed policy published")
	}
	if _, err := service.Resize(t.Context(), ResizeDatabaseRequest{AccountID: request.AccountID, DatabaseID: request.DatabaseID, RequestID: request.RequestID, TargetClass: database.Spec.Class}); !errors.Is(err, ErrConflict) {
		t.Fatal("UUID changed operation kind", err)
	}
	if _, err := service.GetResize(t.Context(), request.AccountID, request.DatabaseID, request.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("policy exposed as resize", err)
	}
	if _, err := service.Resize(t.Context(), resizeRequest(database)); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent resize", err)
	}
	if _, err := service.Delete(t.Context(), database.AccountID, database.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent delete", err)
	}
	provider.lostResponse = true
	if _, err := service.Reconcile(t.Context(), database.AccountID, database.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lost response", err)
	}
	*now = now.Add(time.Minute)
	restarted, err := NewService(service.registry, store, ServiceOptions{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Reconcile(t.Context(), database.AccountID, database.ID); err != nil {
		t.Fatal(err)
	}
	result, _ := store.Get(t.Context(), database.AccountID, database.ID)
	want := database.Spec
	want.ScaleToZero = request.ScaleToZero
	if result.Spec != want || result.DataResourceID != database.DataResourceID || result.ProviderResourceID != database.ProviderResourceID || provider.patches != 1 {
		t.Fatal("policy changed data/class or repeated mutation", result)
	}
	operation, err = restarted.ChangeComputePolicy(t.Context(), request)
	if err != nil || operation.State != ResizeSucceeded {
		t.Fatal("closed admission replay", operation, err)
	}
	request.ScaleToZero = !request.ScaleToZero
	if _, err := restarted.ChangeComputePolicy(t.Context(), request); !errors.Is(err, ErrConflict) {
		t.Fatal("UUID changed target", err)
	}
}

func TestComputePolicyBidirectionalAndCapabilityAdmission(t *testing.T) {
	service, store, provider, database, now := policyFixture(t)
	for _, target := range []bool{false, true, true} {
		request := ChangeComputePolicyRequest{AccountID: database.AccountID, DatabaseID: database.ID, RequestID: uuid.NewString(), ScaleToZero: target}
		if _, err := service.ChangeComputePolicy(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Minute)
		if _, err := service.Reconcile(t.Context(), database.AccountID, database.ID); err != nil {
			t.Fatal(err)
		}
		saved, _ := store.Get(t.Context(), database.AccountID, database.ID)
		if saved.Spec.ScaleToZero != target || saved.Spec.Class != database.Spec.Class {
			t.Fatal(saved)
		}
	}
	backend := service.registry.backends[database.BackendID]
	backend.Capabilities.ScaleToZeroUpdate = false
	service.registry.backends[backend.ID] = backend
	calls := provider.calls
	if _, err := service.ChangeComputePolicy(t.Context(), ChangeComputePolicyRequest{AccountID: database.AccountID, DatabaseID: database.ID, RequestID: uuid.NewString(), ScaleToZero: false}); !errors.Is(err, ErrUnsupported) || provider.calls != calls {
		t.Fatal("unsupported backend admitted", err)
	}
}

func TestComputePolicyDoesNotRequireClassResizeCapability(t *testing.T) {
	service, _, _, database, _ := policyFixture(t)
	backend := service.registry.backends[database.BackendID]
	backend.Capabilities.ClassResize = false
	backend.Capabilities.ServiceClasses = []ServiceClass{database.Spec.Class}
	if backend.Capabilities.Validate() != nil {
		t.Fatal("single-class policy provider rejected")
	}
	service.registry.backends[backend.ID] = backend
	request := ChangeComputePolicyRequest{AccountID: database.AccountID, DatabaseID: database.ID, RequestID: uuid.NewString(), ScaleToZero: false}
	if _, err := service.ChangeComputePolicy(t.Context(), request); err != nil {
		t.Fatal("policy depended on class support", err)
	}
	if _, err := service.Reconcile(t.Context(), database.AccountID, database.ID); err != nil {
		t.Fatal(err)
	}
	backend.Capabilities.ScaleToZero = false
	if backend.Capabilities.Validate() == nil {
		t.Fatal("update without scale-to-zero creation support")
	}
}
