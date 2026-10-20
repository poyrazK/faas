package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreAppVisibilityDefaultsAndUpdates(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "visibility@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "visibility-app"})
	if err != nil {
		t.Fatal(err)
	}
	if app.Visibility != api.AppVisibilityPublic {
		t.Fatalf("default visibility=%q, want public", app.Visibility)
	}
	internal := api.AppVisibilityInternal
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Visibility: &internal, SetVisibility: true})
	if err != nil {
		t.Fatal(err)
	}
	if app.Visibility != api.AppVisibilityInternal {
		t.Fatalf("updated visibility=%q, want internal", app.Visibility)
	}
}
