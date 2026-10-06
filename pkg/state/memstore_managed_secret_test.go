package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
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
		ManagedPostgresAccess:       "read_write",
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

func TestMemStoreManagedPostgresSecretMutationsRefreshRuntimeConfig(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "managed-secret-freshness-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{
		AccountID: account.ID,
		Slug:      "managed-secret-" + uuid.NewString()[:8],
		Type:      AppTypeApp,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, Deployment{
		AppID:       app.ID,
		Kind:        DeploymentKindImage,
		ImageDigest: "sha256:managed-postgres-freshness",
		Status:      DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	createSnapshots := func(label string) {
		t.Helper()
		for _, tier := range []string{SnapshotTierInit, SnapshotTierWarm} {
			if _, err := store.CreateSnapshot(ctx, Snapshot{
				DeploymentID: deployment.ID,
				FCVersion:    "fc-test",
				MemBytes:     1024,
				DiskBytes:    512,
				StorageKey:   "managed-postgres/" + label + "/" + tier,
				Tier:         tier,
			}); err != nil {
				t.Fatalf("create %s snapshot: %v", tier, err)
			}
		}
	}
	assertSnapshotsFresh := func(wantFresh bool) {
		t.Helper()
		for _, tier := range []string{SnapshotTierInit, SnapshotTierWarm} {
			_, err := store.LatestSnapshotForTier(ctx, deployment.ID, tier)
			if wantFresh && err != nil {
				t.Errorf("%s snapshot should be restorable: %v", tier, err)
			}
			if !wantFresh && !errors.Is(err, ErrNotFound) {
				t.Errorf("%s snapshot remains restorable after binding change: %v", tier, err)
			}
		}
	}
	createSnapshots("before-create")
	if _, stamped, err := store.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || stamped {
		t.Fatalf("initial runtime stamp: stamped=%v err=%v", stamped, err)
	}
	secret := AppSecret{
		AccountID:                   account.ID,
		AppID:                       app.ID,
		Scope:                       "production",
		Key:                         "DATABASE_URL",
		Ciphertext:                  []byte("sealed-db-url"),
		Kid:                         "age1test",
		ValueHash:                   "0123456789abcdef",
		ManagedPostgresBindingID:    "binding-a",
		ManagedPostgresAccess:       "read_write",
		ManagedCredentialRef:        "credential-a",
		ManagedCredentialGeneration: 1,
	}
	if err := store.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatalf("put managed secret: %v", err)
	}
	firstStamp, stamped, err := store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || !stamped {
		t.Fatalf("create runtime stamp: %v %v %v", firstStamp, stamped, err)
	}
	assertSnapshotsFresh(false)
	if err := store.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatalf("idempotent managed secret put: %v", err)
	}
	if retryStamp, _, err := store.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || !retryStamp.Equal(firstStamp) {
		t.Fatalf("idempotent put moved runtime stamp from %v to %v: %v", firstStamp, retryStamp, err)
	}
	conflict := secret
	conflict.ManagedCredentialRef = "credential-other"
	if err := store.PutManagedPostgresSecret(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting managed secret put = %v, want ErrConflict", err)
	}
	if afterConflict, _, err := store.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || !afterConflict.Equal(firstStamp) {
		t.Fatalf("conflicting put moved runtime stamp: %v (want %v), err=%v", afterConflict, firstStamp, err)
	}

	createSnapshots("before-delete")
	time.Sleep(time.Millisecond)
	if err := store.DeleteManagedPostgresSecret(ctx, secret.ManagedCredentialRef); err != nil {
		t.Fatalf("delete managed secret: %v", err)
	}
	deleteStamp, stamped, err := store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || !stamped || !deleteStamp.After(firstStamp) {
		t.Fatalf("delete runtime stamp: %v (previous %v), stamped=%v err=%v", deleteStamp, firstStamp, stamped, err)
	}
	assertSnapshotsFresh(false)
	if err := store.DeleteManagedPostgresSecret(ctx, secret.ManagedCredentialRef); err != nil {
		t.Fatalf("idempotent managed secret delete: %v", err)
	}
	if retryStamp, _, err := store.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || !retryStamp.Equal(deleteStamp) {
		t.Fatalf("idempotent delete moved runtime stamp from %v to %v: %v", deleteStamp, retryStamp, err)
	}
}
