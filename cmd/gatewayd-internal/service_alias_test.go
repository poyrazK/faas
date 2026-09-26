// adr: 269
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceAliasAllowedUsesCurrentBindingInventory(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "alias@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	caller, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "frontend", Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive,
		Manifest: state.AppManifest{
			ServiceBindingPolicy: api.ServiceBindingPolicyAccount,
			ServiceBindings:      []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := newServiceAliasAllowed(store)
	for _, tc := range []struct {
		caller string
		name   string
		want   bool
	}{
		{caller.ID, "billing", true},
		{caller.ID, "identity", false},
		{"not-a-uuid", "billing", false},
		{"00000000-0000-4000-8000-000000000000", "billing", false},
	} {
		got, err := allowed(ctx, tc.caller, tc.name)
		if err != nil || got != tc.want {
			t.Fatalf("alias %q/%q = %v, %v; want %v", tc.caller, tc.name, got, err, tc.want)
		}
	}
	manifest := caller.Manifest
	manifest.ServiceBindings = nil
	if _, err := store.UpdateApp(ctx, caller.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if got, err := allowed(ctx, caller.ID, "billing"); err != nil || got {
		t.Fatalf("removed binding still allowed: %v, %v", got, err)
	}
	manifest.ServiceBindings = []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"}}
	deleted := state.AppDeleted
	if _, err := store.UpdateApp(ctx, caller.ID, state.UpdateAppParams{Manifest: &manifest, Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	if got, err := allowed(ctx, caller.ID, "billing"); err != nil || got {
		t.Fatalf("deleted caller alias allowed: %v, %v", got, err)
	}
}

type failingServiceAliasStore struct{ state.Store }

func (f failingServiceAliasStore) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, errors.New("store unavailable")
}

func TestServiceAliasAllowedStoreFailure(t *testing.T) {
	allowed := newServiceAliasAllowed(failingServiceAliasStore{Store: state.NewMemStore()})
	if ok, err := allowed(context.Background(), "00000000-0000-4000-8000-000000000001", "billing"); err == nil || ok {
		t.Fatalf("store failure = %v, %v", ok, err)
	}
}
