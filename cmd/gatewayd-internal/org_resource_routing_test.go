// adr: 202610171904
package main

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgResourceServiceIsolation(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	creator, err := store.CreateAccount(ctx, "org-route-creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateAccount(ctx, "org-route-member@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateOrg(ctx, state.Org{Slug: "route-company", Name: "Company", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateOrg(ctx, state.Org{Slug: "route-other", Name: "Other", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	create := func(namespace, orgID, slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: namespace, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller := create(creator.ID, company.ID, "company-caller")
	sibling := create(creator.ID, company.ID, "company-sibling")
	colleague := create(member.ID, company.ID, "company-colleague")
	foreign := create(creator.ID, other.ID, "other-company-app")
	actor := state.ServiceAddressCaller{AppID: caller.ID, AccountID: caller.AccountID}
	resolve := newServiceProxyResolver(store)
	authorize := newServiceProxyAuthorizer(store)
	tcp := newServiceTCPTargetResolver(store)
	for _, target := range []state.App{sibling, colleague} {
		if _, err := authorize(ctx, caller.ID, target.ID); err != nil {
			t.Fatalf("company HTTP service %s: %v", target.Slug, err)
		}
	}
	if _, err := authorize(ctx, caller.ID, foreign.ID); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatalf("cross-company HTTP access through a shared namespace: %v", err)
	}
	for _, target := range []state.App{sibling, colleague, foreign} {
		_, found := resolveServiceAddress(ctx, store, resolve, actor, target.Slug, slog.Default())
		if want := target.ID == sibling.ID; found != want {
			t.Fatalf("synthetic address for %s = %v, want %v", target.Slug, found, want)
		}
	}
	for _, target := range []state.App{sibling, foreign} {
		index, err := store.AppServiceAddressIndex(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		address, _ := api.ServiceAddressForIndex(index)
		got, found, err := tcp(ctx, caller.ID, address)
		if want := target.ID == sibling.ID; err != nil || found != want || (found && got.AppID != target.ID) {
			t.Fatalf("TCP address for %s: %+v %v %v", target.Slug, got, found, err)
		}
	}
}
