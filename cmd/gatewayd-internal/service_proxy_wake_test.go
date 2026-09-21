// adr: 196
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// The waker must hand the scheduler the target's own plan and identity —
// ensureCapacity derives the wake queue cap and concurrency ceiling from them,
// so a wrong projection silently applies the wrong app's wake policy.
func TestServiceProxyWakerProjectsTargetApp(t *testing.T) {
	store := state.NewMemStore()
	app := seedApp(t, store, "orders", api.PlanPro)

	var got gateway.App
	var calls int
	wake := newServiceProxyWaker(store, func(_ context.Context, resolved gateway.App) error {
		calls++
		got = resolved
		return nil
	})

	if err := wake(context.Background(), app.ID); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if calls != 1 {
		t.Fatalf("ensure calls = %d, want 1", calls)
	}
	if got.ID != app.ID {
		t.Errorf("app id = %q, want %q", got.ID, app.ID)
	}
	if got.Plan != api.PlanPro {
		t.Errorf("plan = %q, want pro", got.Plan)
	}
	if got.AccountID != app.AccountID {
		t.Errorf("account = %q, want %q", got.AccountID, app.AccountID)
	}
}

// An internal app is invisible to the public hostname resolver by design
// (ADR-119). The service mesh is exactly the path that must still reach it, so
// the waker must not inherit that exclusion.
func TestServiceProxyWakerWakesInternalApp(t *testing.T) {
	store := state.NewMemStore()
	app := seedApp(t, store, "auth", api.PlanPro)
	internal := api.AppVisibilityInternal
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		Visibility: &internal, SetVisibility: true,
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}

	var calls int
	wake := newServiceProxyWaker(store, func(context.Context, gateway.App) error {
		calls++
		return nil
	})

	if err := wake(context.Background(), app.ID); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if calls != 1 {
		t.Errorf("ensure calls = %d, want 1 for an internal app", calls)
	}
}

// A customer deleting an app between resolution and wake is not a platform
// failure. Returning an error here would surface "service could not be woken"
// instead of letting the proxy report that there is no replica.
func TestServiceProxyWakerIgnoresDeletedApp(t *testing.T) {
	store := state.NewMemStore()
	app := seedApp(t, store, "billing", api.PlanPro)
	if err := store.DeleteApp(context.Background(), app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	var calls int
	wake := newServiceProxyWaker(store, func(context.Context, gateway.App) error {
		calls++
		return nil
	})

	if err := wake(context.Background(), app.ID); err != nil {
		t.Fatalf("wake on a deleted app = %v, want nil", err)
	}
	if calls != 0 {
		t.Errorf("ensure calls = %d, want 0 for a deleted app", calls)
	}
}

// A store failure must name the app so an operator reading the 503's log line
// knows which dependency could not be woken.
func TestServiceProxyWakerReportsUnknownApp(t *testing.T) {
	wake := newServiceProxyWaker(state.NewMemStore(), func(context.Context, gateway.App) error {
		t.Fatal("ensure must not run when the app cannot be loaded")
		return nil
	})

	err := wake(context.Background(), "app-missing")
	if err == nil {
		t.Fatal("wake on an unknown app = nil, want an error")
	}
	if !strings.Contains(err.Error(), "app-missing") {
		t.Errorf("error = %q, want it to name the app", err.Error())
	}
}
