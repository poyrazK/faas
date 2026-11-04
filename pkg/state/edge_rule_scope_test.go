package state_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-31 (round 6): a company app keeps its
// creator's account, so the gateway keeps a host's rules to its app's
// edge-rule scope: the owning organization for a company app, "" for a
// personal app (no organization, or the account's personal one).
func TestEdgeRuleScopes(t *testing.T) {
	t.Run("mem", func(t *testing.T) {
		exerciseEdgeRuleScopes(t, t.Context(), state.NewMemStore())
	})
	t.Run("pg", func(t *testing.T) {
		s, ctx := pgStore(t)
		exerciseEdgeRuleScopes(t, ctx, s)
	})
}

type edgeRuleScopeTestStore interface {
	CreateAccountWithPersonalOrg(ctx context.Context, p state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
	CreateOrg(ctx context.Context, o state.Org) (state.Org, error)
	CreateApp(ctx context.Context, a state.App) (state.App, error)
	OrgByID(ctx context.Context, id string) (state.Org, error)
	EdgeRuleScopes(ctx context.Context, appIDs []string) (map[string]string, error)
}

func exerciseEdgeRuleScopes(t *testing.T, ctx context.Context, s edgeRuleScopeTestStore) {
	t.Helper()
	created, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "scope-" + uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	acct := created.Account
	company, err := s.CreateOrg(ctx, state.Org{Slug: "scope-" + uuid.NewString()[:8], Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app := func(name, orgID string) state.App {
		t.Helper()
		a, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: orgID, Slug: name + "-" + uuid.NewString()[:8], Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	personal := app("personal", "")
	inPersonalOrg := app("in-personal-org", created.PersonalOrg.ID)
	companyApp := app("company", company.ID)
	unknown := uuid.NewString()

	scopes, err := s.EdgeRuleScopes(ctx, []string{personal.ID, inPersonalOrg.ID, companyApp.ID, unknown})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{personal.ID: "", inPersonalOrg.ID: "", companyApp.ID: company.ID} {
		if got, ok := scopes[id]; !ok || got != want {
			t.Errorf("scope of %s = %q (known %v), want %q", id, got, ok, want)
		}
	}
	if _, ok := scopes[unknown]; ok {
		t.Error("an unknown app got a scope")
	}
	for _, a := range []state.App{personal, inPersonalOrg, companyApp} {
		got, err := state.AppEdgeRuleScope(ctx, s, a)
		if err != nil || got != scopes[a.ID] {
			t.Errorf("AppEdgeRuleScope(%s) = %q, %v; want %q like EdgeRuleScopes", a.Slug, got, err, scopes[a.ID])
		}
	}
}
