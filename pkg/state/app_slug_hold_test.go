package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A renamed or purged app's slug stays held for its account: another account
// can neither create an app with it nor rename an app to it, while the owner
// may take it back (bug hunt 2026-10-28 H80, ADR-20261101091049).
func TestMemStoreAppSlugHolds(t *testing.T) {
	assertAppSlugHolds(t, context.Background(), state.NewMemStore())
}

func TestPgStoreAppSlugHolds(t *testing.T) {
	store, ctx := pgStore(t)
	assertAppSlugHolds(t, ctx, store)
}

func assertAppSlugHolds(t *testing.T, ctx context.Context, store state.Store) {
	t.Helper()
	owner, err := store.CreateAccount(ctx, "slug-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "slug-other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	suffix := uuid.NewString()[:8]
	renamedFrom, renamedTo, purged := "web-"+suffix, "web2-"+suffix, "gone-"+suffix
	create := func(acct state.Account, slug string) (state.App, error) {
		return store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: slug})
	}

	if _, err := create(owner, renamedFrom); err != nil {
		t.Fatalf("create %s: %v", renamedFrom, err)
	}
	if _, err := store.RenameApp(ctx, owner.ID, renamedFrom, renamedTo); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := create(other, renamedFrom); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("another account created the renamed app's old slug: %v, want ErrConflict", err)
	}
	decoy, err := create(other, "decoy-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RenameApp(ctx, other.ID, decoy.Slug, renamedFrom); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("another account renamed onto the held slug: %v, want ErrConflict", err)
	}
	if _, err := store.RenameApp(ctx, owner.ID, renamedTo, renamedFrom); err != nil {
		t.Fatalf("owner could not take its old slug back: %v", err)
	}

	gone, err := create(owner, purged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScheduleAppDeletion(ctx, gone.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}
	if err := store.ClaimAppDeletion(ctx, gone.ID); err != nil {
		t.Fatalf("claim deletion: %v", err)
	}
	if err := store.DeleteAppPermanently(ctx, gone.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := create(other, purged); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("another account created a purged app's slug: %v, want ErrConflict", err)
	}
	if _, err := create(owner, purged); err != nil {
		t.Fatalf("owner could not recreate its purged app's slug: %v", err)
	}
}
