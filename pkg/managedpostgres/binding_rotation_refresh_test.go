package managedpostgres

import (
	"errors"
	"testing"
	"time"
)

func TestBindingRotationRefreshSurvivesProviderFailureAndWorkerRestart(t *testing.T) {
	now := time.Now().UTC()
	enabled := true
	provider := &bindingProvider{}
	store := &failFinishBindingStore{MemoryStore: NewMemoryStore()}
	service, _, database := readyBindingFixture(t, provider, store, &now, &enabled)
	binding, err := service.Create(t.Context(), CreateBindingRequest{AccountID: database.AccountID, DatabaseID: database.ID, AppID: "app-a", Scope: "production", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	provider.credentialErr = ErrUnavailable
	if _, err := service.Rotate(t.Context(), binding.AccountID, binding.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("provider rejection missing", err)
	}
	provider.credentialErr = nil
	now = now.Add(2 * time.Hour)
	// A replacement worker has only the persisted catalog and secret sink.
	service, err = NewBindingService(service.registry, store, store, service.sink, BindingServiceOptions{Now: func() time.Time { return now }, ProvisioningEnabled: func() bool { return enabled }})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewBindingReconciler(service, BindingReconcilerOptions{Now: func() time.Time { return now }, IncludeProvisioning: func() bool { return enabled }})
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := worker.Sweep(t.Context()); err != nil || summary.Completed != 1 {
		t.Fatal(summary, err)
	}
	intents := store.PendingBindingRotationRefreshes()
	if len(intents) != 1 || intents[0].BindingID != binding.ID || intents[0].WakeID == "" || provider.revokeCalls != 0 {
		t.Fatalf("recovered rotation lost its handoff or retired early: %+v revoked=%d", intents, provider.revokeCalls)
	}
	for range 3 {
		if _, err := service.Rotate(t.Context(), binding.AccountID, binding.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.PendingBindingRotationRefreshes()) != 1 || provider.issueCalls != 3 {
		t.Fatal("retry minted another generation or refresh")
	}
	// Model a ready receipt left by N-1 before it dispatched the refresh.
	store.mu.Lock()
	delete(store.rotationRefreshes, binding.ID)
	store.mu.Unlock()
	enabled = false
	if summary, err := worker.Sweep(t.Context()); err != nil || summary.Discovered != 1 || len(store.PendingBindingRotationRefreshes()) != 1 {
		t.Fatalf("legacy handoff recovery stopped at the provisioning gate: %+v %v", summary, err)
	}
	// Only the runtime acknowledgement authorizes retirement.
	store.mu.Lock()
	current := store.bindings[binding.ID]
	current.RotationCleanupReady = true
	store.bindings[binding.ID] = current
	store.mu.Unlock()
	if _, err := worker.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err = store.GetBinding(t.Context(), binding.AccountID, binding.ID)
	if err != nil || current.RotationPreviousGeneration != 0 || provider.revokeCalls != 1 || len(store.PendingBindingRotationRefreshes()) != 0 {
		t.Fatalf("acknowledged rotation did not retire its old identity: %+v %v", current, err)
	}
}
