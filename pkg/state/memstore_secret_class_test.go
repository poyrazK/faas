package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreAppSecretClassDefaultsAndSurvivesLegacyWrites(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)

	if err := store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "production", "TOKEN", "kid", "hash", state.SecretClassEphemeral, []byte("sealed-v1")); err != nil {
		t.Fatalf("classified upsert: %v", err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "production", "TOKEN", "kid-2", "hash-2", []byte("sealed-v2")); err != nil {
		t.Fatalf("legacy upsert: %v", err)
	}
	row, err := store.GetAppSecretInScope(ctx, account.ID, app.ID, "production", "TOKEN")
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if row.SecretClass != state.SecretClassEphemeral {
		t.Fatalf("legacy upsert class = %q, want ephemeral", row.SecretClass)
	}

	if err := store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "production", "TOKEN", "kid-3", "hash-3", state.SecretClassPersistent, []byte("sealed-v3")); err != nil {
		t.Fatalf("explicit persistent upsert: %v", err)
	}
	row, err = store.GetAppSecretInScope(ctx, account.ID, app.ID, "production", "TOKEN")
	if err != nil || row.SecretClass != state.SecretClassPersistent {
		t.Fatalf("explicit persistent class = %v, err=%v", row, err)
	}
}

func TestMemStoreAppSecretClassRejectsUnknownValue(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)
	if err := store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "default", "TOKEN", "kid", "hash", "temporary", []byte("sealed")); err != state.ErrInvalidArgument {
		t.Fatalf("unknown class error = %v, want ErrInvalidArgument", err)
	}
}
