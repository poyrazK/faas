package state

import (
	"context"
	"errors"
	"testing"
)

func TestMemStoreManagedSecretRejectsCustomerMutationsButAllowsReseal(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	address := secretKey{AppID: "app-a", Scope: "production", Key: "DATABASE_URL"}
	managed := AppSecret{
		AccountID:                   "account-a",
		AppID:                       address.AppID,
		Scope:                       address.Scope,
		Key:                         address.Key,
		Ciphertext:                  []byte("managed-ciphertext"),
		Kid:                         "age1old",
		ManagedPostgresBindingID:    "binding-a",
		ManagedCredentialRef:        "credential-a",
		ManagedCredentialGeneration: 1,
	}
	if err := store.PutManagedPostgresSecret(ctx, managed); err != nil {
		t.Fatalf("put managed secret: %v", err)
	}
	if err := store.PutManagedPostgresSecret(ctx, managed); err != nil {
		t.Fatalf("idempotent managed secret put: %v", err)
	}
	conflicting := managed
	conflicting.ManagedCredentialRef = "credential-b"
	if err := store.PutManagedPostgresSecret(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-generation credential replacement = %v, want ErrConflict", err)
	}

	mutations := []struct {
		name string
		run  func() error
	}{
		{
			name: "ciphertext",
			run: func() error {
				return store.UpsertAppSecretInScope(ctx, "account-a", address.AppID, address.Scope, address.Key, []byte("customer"))
			},
		},
		{
			name: "kid",
			run: func() error {
				return store.UpsertAppSecretWithKidInScope(ctx, "account-a", address.AppID, address.Scope, address.Key, "age1customer", []byte("customer"))
			},
		},
		{
			name: "value hash",
			run: func() error {
				return store.UpsertAppSecretWithKidAndValueHashInScope(ctx, "account-a", address.AppID, address.Scope, address.Key, "age1customer", "0123456789abcdef", []byte("customer"))
			},
		},
		{
			name: "delete",
			run: func() error {
				return store.DeleteAppSecretInScope(ctx, "account-a", address.AppID, address.Scope, address.Key)
			},
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if err := mutation.run(); !errors.Is(err, ErrConflict) {
				t.Fatalf("managed secret mutation = %v, want ErrConflict", err)
			}
		})
	}

	if err := store.ResealAppSecretWithKidAndValueHashInScope(ctx, "account-a", address.AppID, address.Scope, address.Key, "age1new", "fedcba9876543210", []byte("resealed")); err != nil {
		t.Fatalf("maintenance reseal: %v", err)
	}
	got := store.secrets[address]
	if got.ManagedPostgresBindingID != "binding-a" || got.ManagedCredentialRef != "credential-a" || got.ManagedCredentialGeneration != 1 {
		t.Fatalf("reseal changed ownership: %+v", got)
	}
	if got.Kid != "age1new" || got.ValueHash != "fedcba9876543210" || string(got.Ciphertext) != "resealed" {
		t.Fatalf("reseal did not update envelope: %+v", got)
	}
	if err := store.DeleteManagedPostgresSecret(ctx, "credential-a"); err != nil {
		t.Fatalf("delete managed secret: %v", err)
	}
	if err := store.DeleteManagedPostgresSecret(ctx, "credential-a"); err != nil {
		t.Fatalf("idempotent managed secret delete: %v", err)
	}
}
