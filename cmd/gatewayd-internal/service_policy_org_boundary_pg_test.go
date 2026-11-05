package main

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 23 H23-1: the pinned policy view every service call is authorized from
// read apps without org_id, so the tenant check compared only creators. One
// person's apps in two orgs, or in an org and their personal account, called
// each other's internal services by slug, and kept doing so after the person
// left the org, because an app keeps its creator's account_id.
func TestServicePolicyPinnerKeepsTheOrgBoundary(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account := func(email string) state.Account {
		t.Helper()
		created, err := store.CreateAccount(ctx, email, api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	org := func(slug string) state.Org {
		t.Helper()
		created, err := store.CreateOrg(ctx, state.Org{Slug: slug, Name: slug, Plan: api.PlanPro})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	member, colleague := account("org-boundary-member@test.local"), account("org-boundary-colleague@test.local")
	orgA, orgB := org("org-boundary-a"), org("org-boundary-b")
	app := func(accountID, orgID, slug string) state.App {
		t.Helper()
		created, err := store.CreateApp(ctx, state.App{AccountID: accountID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	personal := app(member.ID, "", "org-boundary-personal")
	orgAAPI := app(member.ID, orgA.ID, "org-boundary-a-api")
	orgAWorker := app(colleague.ID, orgA.ID, "org-boundary-a-worker")
	orgBAPI := app(member.ID, orgB.ID, "org-boundary-b-api")
	pin := newServicePolicyPinner(store)
	for _, tc := range []struct {
		name           string
		caller, target state.App
		denied         bool
	}{
		{"a personal app calls its creator's org service", personal, orgAAPI, true},
		{"an org service calls its creator's personal app", orgAAPI, personal, true},
		{"one org calls another with the same creator", orgAAPI, orgBAPI, true},
		{"two creators' services inside one org", orgAAPI, orgAWorker, false},
	} {
		snapshot, err := pin(ctx, tc.caller.ID, tc.target.Slug, false)
		if err != nil || !snapshot.Found || snapshot.Target.AppID != tc.target.ID {
			t.Fatalf("%s: snapshot = %+v, %v", tc.name, snapshot, err)
		}
		if tc.denied && !errors.Is(snapshot.AuthorizationError, gateway.ErrServiceProxyDenied) ||
			!tc.denied && snapshot.AuthorizationError != nil {
			t.Errorf("%s: authorization = %v, want denied %v", tc.name, snapshot.AuthorizationError, tc.denied)
		}
	}
}
