// adr: 1137
package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Declared wake-ahead targets are exactly the services the caller could call:
// resolved and authorized like a call, in declaration order, capped.
func TestWakeAheadDeclaredResolvesReachableDependencies(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "wake-ahead-declared@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(accountID, slug string, manifest state.AppManifest, previewOf string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: slug, Type: state.AppTypeApp,
			RAMMB: 128, Status: state.AppActive, Manifest: manifest, PreviewOfSlug: previewOf})
		if err != nil {
			t.Fatalf("CreateApp %q: %v", slug, err)
		}
		return app
	}
	declares := func(services ...string) state.AppManifest {
		manifest := state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared}
		for _, service := range services {
			manifest.ServiceBindings = append(manifest.ServiceBindings, api.AppServiceBinding{Binding: "GREGALE_SERVICE_URL", Service: service})
		}
		return manifest
	}
	auth := newApp(acct.ID, "auth", state.AppManifest{}, "")
	billing := newApp(acct.ID, "billing", state.AppManifest{}, "")
	ledger := newApp(acct.ID, "ledger", state.AppManifest{}, "")
	newApp(acct.ID, "extra", state.AppManifest{}, "")
	noCallers := []string{}
	newApp(acct.ID, "closed", state.AppManifest{AllowedServiceCallers: &noCallers}, "")
	caller := newApp(acct.ID, "api", declares("auth", "billing", "closed", "missing", "api", "auth", " ledger ", "extra"), "")
	preview := newApp(acct.ID, "pr-7-api", declares("auth"), "api")
	other, err := store.CreateAccount(ctx, "wake-ahead-other@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	outsider := newApp(other.ID, "outsider", declares("auth"), "")

	declared := newWakeAheadDeclared(store, newServiceProxyResolver(store), newServiceProxyAuthorizer(store))
	tests := []struct {
		name   string
		caller string
		want   []string
	}{
		{"denied, unknown, self and repeated names are skipped; three at most", caller.ID, []string{auth.ID, billing.ID, ledger.ID}},
		{"a preview caller declares nothing", preview.ID, nil},
		{"another account's service is not reachable", outsider.ID, nil},
		{"an unknown caller declares nothing", "00000000-0000-4000-8000-000000000000", nil},
		{"a malformed caller declares nothing", "not-an-app", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := declared(ctx, tt.caller)
			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("declared = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestWakeAheadDeclaredReportsStoreFailures(t *testing.T) {
	boom := errors.New("store down")
	store := failingAppStore{Store: state.NewMemStore(), err: boom}
	declared := newWakeAheadDeclared(store, newServiceProxyResolver(store), newServiceProxyAuthorizer(store))
	if _, err := declared(context.Background(), "00000000-0000-4000-8000-000000000001"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store failure", err)
	}
}
