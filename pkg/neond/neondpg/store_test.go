// adr: 20261112160000 — neon_project_keys holds one live key per project and
// keeps revoked keys as the record of what was issued.
package neondpg_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/neond/neondpg"
)

func TestStoreProjectKeys(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := neondpg.New(pool)

	if _, ok, err := store.LiveProjectKey(ctx, "p-1"); ok || err != nil {
		t.Fatalf("empty table: ok=%v err=%v", ok, err)
	}
	if err := store.InsertProjectKey(ctx, "p-1", 11, []byte("sealed-11")); err != nil {
		t.Fatal(err)
	}
	// A second live key for the same project is refused.
	if err := store.InsertProjectKey(ctx, "p-1", 12, []byte("sealed-12")); err == nil {
		t.Fatal("second live key for p-1 stored")
	}
	// A project ID Neon never issues is refused by the CHECK.
	if err := store.InsertProjectKey(ctx, "p/../x", 13, []byte("sealed")); err == nil {
		t.Fatal("malformed project ID stored")
	}
	got, ok, err := store.LiveProjectKey(ctx, "p-1")
	if err != nil || !ok || got.KeyID != 11 || string(got.Sealed) != "sealed-11" {
		t.Fatalf("live key = %+v %v %v", got, ok, err)
	}
	if err := store.MarkProjectKeyRevoked(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if ids, err := store.LiveProjectKeyIDs(ctx, "p-1"); err != nil || len(ids) != 0 {
		t.Fatalf("live ids after revoke = %v %v", ids, err)
	}
	// Once the old key is revoked a new one may be stored.
	if err := store.InsertProjectKey(ctx, "p-1", 12, []byte("sealed-12")); err != nil {
		t.Fatalf("replacement key: %v", err)
	}
	if ids, err := store.LiveProjectKeyIDs(ctx, "p-1"); err != nil || len(ids) != 1 || ids[0] != 12 {
		t.Fatalf("live ids = %v %v", ids, err)
	}
}
