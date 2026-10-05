package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingPromotionMemoryCatalogFencesRotationAndHoldsLock(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	acct, app := uuid.NewString(), uuid.NewString()
	database := readyMemoryDatabase(t, store, acct, uuid.NewString(), "primary", time.Now().UTC())
	binding := testBinding(acct, database.ID, app, uuid.NewString(), time.Now().UTC())
	if _, _, err := store.ReserveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	now := time.Now().UTC()
	if _, err := store.ClaimBinding(ctx, acct, binding.ID, token, BindingStateProvisioning, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishBindingProvision(ctx, binding.ID, token, "private-provider", "private-reference", now); err != nil {
		t.Fatal(err)
	}
	fence, err := store.ReadPromotionFence(ctx, acct, app)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginBindingRotation(ctx, acct, binding.ID, uuid.NewString(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := store.GuardPromotion(ctx, acct, app, fence, nil, func(context.Context) error { called = true; return nil }); !errors.Is(err, ErrConflict) || called {
		t.Fatalf("rotation did not invalidate fence: called=%v err=%v", called, err)
	}
	fence, err = store.ReadPromotionFence(ctx, acct, app)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- store.GuardPromotion(ctx, acct, app, fence, nil, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	read := make(chan struct{})
	go func() { _, _ = store.ReadPromotionFence(ctx, acct, app); close(read) }()
	select {
	case <-read:
		t.Fatal("catalog lock released before traffic callback")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-read
}

func TestBindingPromotionPostgresRequiresSharedPool(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	fence, err := store.ReadPromotionFence(ctx, accountID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	callback := func(context.Context) error { called = true; return nil }
	if err := store.GuardPromotion(ctx, accountID, "", fence, nil, callback); !errors.Is(err, ErrUnsupported) || called {
		t.Fatalf("separate backend permitted: %v", err)
	}
	if err := store.GuardPromotion(ctx, accountID, "", fence, pool, callback); err != nil || !called {
		t.Fatalf("shared backend rejected: %v", err)
	}
}

func TestBindingPromotionPostgresRevisionTracksRotationBeforeRuntimeStamp(t *testing.T) {
	store, pool, ctx, acct := postgresStoreFixture(t)
	stateStore := state.NewPgStore(pool)
	app, err := stateStore.CreateApp(ctx, state.App{AccountID: acct, Slug: "gate-rotation-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	database := postgresReadyDatabase(t, store, acct, "promotion-rotation", now)
	binding := testBinding(acct, database.ID, app.ID, uuid.NewString(), now)
	if _, _, err := store.ReserveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := store.ClaimBinding(ctx, acct, binding.ID, token, BindingStateProvisioning, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: acct, AppID: app.ID, Scope: binding.Scope, Key: binding.EnvironmentKey,
		Ciphertext: []byte("sealed-test-value"), Kid: "age1test", ManagedPostgresBindingID: binding.ID, ManagedCredentialRef: "private-ref", ManagedCredentialGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishBindingProvision(ctx, binding.ID, token, "private-provider", "private-ref", now); err != nil {
		t.Fatal(err)
	}
	before, err := stateStore.ReadBindingPromotionRevision(ctx, acct, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	stampBefore, _, err := stateStore.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginBindingRotation(ctx, acct, binding.ID, uuid.NewString(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := stateStore.ReadBindingPromotionRevision(ctx, acct, app.ID)
	if err != nil || after == before {
		t.Fatalf("rotation did not fence pre-stamp config: before=%s after=%s err=%v", before, after, err)
	}
	stampAfter, _, err := stateStore.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || !stampAfter.Equal(stampBefore) {
		t.Fatalf("test relied on rotation moving the runtime stamp: %v -> %v %v", stampBefore, stampAfter, err)
	}
}
