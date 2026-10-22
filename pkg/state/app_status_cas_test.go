package state_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCompareAndSetAppStatusAllowsOneRestartClaim(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "restart-race@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "restart-race", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}

	var claimed atomic.Int32
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ok, err := store.CompareAndSetAppStatus(ctx, app.ID, state.AppActive, state.AppEvictedCold)
			if err != nil {
				t.Errorf("CompareAndSetAppStatus: %v", err)
				return
			}
			if ok {
				claimed.Add(1)
			}
		}()
	}
	workers.Wait()
	if got := claimed.Load(); got != 1 {
		t.Fatalf("successful restart claims = %d, want 1", got)
	}
	updated, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != state.AppEvictedCold {
		t.Fatalf("status = %q, want %q", updated.Status, state.AppEvictedCold)
	}
}
