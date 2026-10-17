package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBindingPromotionBatchChecksEveryMemberAndHoldsOneLock(t *testing.T) {
	ctx := t.Context()
	store := NewMemoryStore()
	account, first, second := uuid.NewString(), uuid.NewString(), uuid.NewString()
	firstFence, err := store.ReadPromotionFence(ctx, account, first)
	if err != nil {
		t.Fatal(err)
	}
	secondFence, err := store.ReadPromotionFence(ctx, account, second)
	if err != nil {
		t.Fatal(err)
	}
	database := readyMemoryDatabase(t, store, account, uuid.NewString(), "batch", time.Now().UTC())
	binding := testBinding(account, database.ID, second, uuid.NewString(), time.Now().UTC())
	if _, _, err := store.ReserveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	selections := []PromotionSelection{{AccountID: account, AppID: first, Fence: firstFence}, {AccountID: account, AppID: second, Fence: secondFence}}
	called := false
	if err := store.GuardPromotions(ctx, selections, nil, func(context.Context) error { called = true; return nil }); !errors.Is(err, ErrConflict) || called {
		t.Fatalf("stale second member permitted: called=%t error=%v", called, err)
	}
	selections[1].Fence, err = store.ReadPromotionFence(ctx, account, second)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- store.GuardPromotions(ctx, selections, nil, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	read := make(chan struct{})
	go func() { _, _ = store.ReadPromotionFence(ctx, account, second); close(read) }()
	select {
	case <-read:
		t.Error("catalog lock released before graph callback")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-read
}
