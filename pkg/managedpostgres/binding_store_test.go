package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func readyMemoryDatabase(t *testing.T, store *MemoryStore, accountID, databaseID, name string, at time.Time) Database {
	t.Helper()
	database := Database{
		ID:                 databaseID,
		AccountID:          accountID,
		Name:               name,
		Spec:               testSpec(),
		BackendID:          "primary-a",
		BackendFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		State:              StateProvisioning,
		DesiredGeneration:  1,
		CreatedAt:          at,
		UpdatedAt:          at,
	}
	database, created, err := store.Reserve(context.Background(), database, 10)
	if err != nil || !created {
		t.Fatalf("reserve database: created=%v database=%+v err=%v", created, database, err)
	}
	claimed, err := store.Claim(context.Background(), accountID, database.ID, "database-worker", StateProvisioning, at, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("claim database: %v", err)
	}
	if err := store.RecordProviderResource(context.Background(), database.ID, claimed.LeaseToken, "provider-"+database.ID, at.Add(time.Second)); err != nil {
		t.Fatalf("record provider database: %v", err)
	}
	database, err = store.FinishProvision(context.Background(), database.ID, claimed.LeaseToken, at.Add(2*time.Second))
	if err != nil {
		t.Fatalf("finish database: %v", err)
	}
	return database
}

func testBinding(accountID, databaseID, appID, id string, at time.Time) Binding {
	return Binding{
		ID:                   id,
		AccountID:            accountID,
		DatabaseID:           databaseID,
		AppID:                appID,
		Scope:                "production",
		EnvironmentKey:       "DATABASE_URL",
		Access:               CredentialReadWrite,
		CredentialGeneration: 1,
		State:                BindingStateProvisioning,
		CreatedAt:            at,
		UpdatedAt:            at,
	}
}

func TestMemoryBindingStoreReservationClaimsTarget(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	start := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	firstDatabase := readyMemoryDatabase(t, store, "account-a", "database-a", "orders", start)
	secondDatabase := readyMemoryDatabase(t, store, "account-a", "database-b", "analytics", start)

	input := testBinding("account-a", firstDatabase.ID, "app-a", "binding-a", start.Add(3*time.Second))
	createdBinding, created, err := store.ReserveBinding(ctx, input)
	if err != nil || !created {
		t.Fatalf("reserve binding: created=%v binding=%+v err=%v", created, createdBinding, err)
	}
	retry := testBinding("account-a", firstDatabase.ID, "app-a", "binding-retry", start.Add(4*time.Second))
	existing, created, err := store.ReserveBinding(ctx, retry)
	if err != nil || created || existing.ID != createdBinding.ID {
		t.Fatalf("idempotent reserve: created=%v binding=%+v err=%v", created, existing, err)
	}

	conflicting := testBinding("account-a", secondDatabase.ID, "app-a", "binding-b", start.Add(4*time.Second))
	if _, _, err := store.ReserveBinding(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("second database at same target = %v, want ErrConflict", err)
	}
	conflicting.DatabaseID = firstDatabase.ID
	conflicting.Access = CredentialReadOnly
	if _, _, err := store.ReserveBinding(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("different access at same target = %v, want ErrConflict", err)
	}
	reusedID := testBinding("account-a", firstDatabase.ID, "app-b", createdBinding.ID, start.Add(5*time.Second))
	if _, _, err := store.ReserveBinding(ctx, reusedID); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused binding ID at another target = %v, want ErrConflict", err)
	}
}

func TestMemoryBindingStoreLeaseRecoveryAndTargetReuse(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	start := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	database := readyMemoryDatabase(t, store, "account-a", "database-a", "orders", start)
	input := testBinding("account-a", database.ID, "app-a", "binding-a", start.Add(3*time.Second))
	binding, created, err := store.ReserveBinding(ctx, input)
	if err != nil || !created {
		t.Fatalf("reserve binding: created=%v binding=%+v err=%v", created, binding, err)
	}

	if due, err := store.DueBindings(ctx, false, 10, input.CreatedAt); err != nil || len(due) != 0 {
		t.Fatalf("disabled provisioning due=%d err=%v", len(due), err)
	}
	if due, err := store.DueBindings(ctx, true, 10, input.CreatedAt); err != nil || len(due) != 1 {
		t.Fatalf("provisioning due=%d err=%v", len(due), err)
	}
	claimed, err := store.ClaimBinding(ctx, input.AccountID, binding.ID, "first-worker", BindingStateProvisioning, input.CreatedAt, input.CreatedAt.Add(time.Minute))
	if err != nil || claimed.AttemptCount != 1 {
		t.Fatalf("claim: binding=%+v err=%v", claimed, err)
	}
	if _, err := store.ClaimBinding(ctx, input.AccountID, binding.ID, "contender", BindingStateProvisioning, input.CreatedAt, input.CreatedAt.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent claim = %v, want ErrConflict", err)
	}

	recoveredAt := input.CreatedAt.Add(2 * time.Minute)
	recovered, err := store.ClaimBinding(ctx, input.AccountID, binding.ID, "recovered-worker", BindingStateProvisioning, recoveredAt, recoveredAt.Add(time.Minute))
	if err != nil || recovered.AttemptCount != 2 {
		t.Fatalf("expired lease recovery: binding=%+v err=%v", recovered, err)
	}
	if _, err := store.FinishBindingProvision(ctx, binding.ID, claimed.LeaseToken, "identity-a", "secret-ref-a", recoveredAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale completion = %v, want ErrConflict", err)
	}
	ready, err := store.FinishBindingProvision(ctx, binding.ID, recovered.LeaseToken, "identity-a", "secret-ref-a", recoveredAt.Add(time.Second))
	if err != nil || ready.State != BindingStateReady || ready.AttemptCount != 0 {
		t.Fatalf("finish provision: binding=%+v err=%v", ready, err)
	}

	deleteAt := recoveredAt.Add(2 * time.Second)
	deleting, err := store.ClaimBinding(ctx, input.AccountID, binding.ID, "delete-worker", BindingStateDeleting, deleteAt, deleteAt.Add(time.Minute))
	if err != nil || deleting.AttemptCount != 1 {
		t.Fatalf("claim delete: binding=%+v err=%v", deleting, err)
	}
	deleted, err := store.FinishBindingDelete(ctx, binding.ID, deleting.LeaseToken, deleteAt.Add(time.Second))
	if err != nil || deleted.State != BindingStateDeleted || deleted.DeletedAt == nil {
		t.Fatalf("finish delete: binding=%+v err=%v", deleted, err)
	}
	if listed, err := store.ListBindings(ctx, input.AccountID, database.ID); err != nil || len(listed) != 0 {
		t.Fatalf("deleted binding listed: bindings=%+v err=%v", listed, err)
	}

	replacement := testBinding(input.AccountID, database.ID, input.AppID, "binding-b", deleteAt.Add(2*time.Second))
	if got, created, err := store.ReserveBinding(ctx, replacement); err != nil || !created || got.ID != replacement.ID {
		t.Fatalf("released target reuse: created=%v binding=%+v err=%v", created, got, err)
	}
}
