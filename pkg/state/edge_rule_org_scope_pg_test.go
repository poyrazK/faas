//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 24 H24-1: a host's edge-rule set is scoped by the host app's resource
// owner (its org, or its account when it has none), not by the app's creator
// account. A company app keeps its creator's account_id, so a rule written on
// another of that creator's apps — in another org, or their personal account,
// even after they left the org — must not ride along on the company app's host.
func TestPgEdgeRulesScopeByResourceOwner(t *testing.T) {
	s, ctx := pgStore(t)
	member, err := s.CreateAccount(ctx, "edge-org-member@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	orgA, err := s.CreateOrg(ctx, state.Org{Slug: "edge-org-a", Name: "Edge org A", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	orgB, err := s.CreateOrg(ctx, state.Org{Slug: "edge-org-b", Name: "Edge org B", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app := func(orgID, slug string) state.App {
		t.Helper()
		created, err := s.CreateApp(ctx, state.App{AccountID: member.ID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(slug, err)
		}
		return created
	}
	orgAApp := app(orgA.ID, "edge-org-a-prod")
	orgBApp := app(orgB.ID, "edge-org-b-prod")
	personal := app("", "edge-org-personal")

	const host = "shop.customer.example"
	mk := func(a state.App) string {
		t.Helper()
		rule, err := s.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
			AccountID: member.ID, AppID: a.ID, MatchHost: host, MatchPath: "/**",
			MatchMethods: []string{}, Priority: 10, Enabled: true, Kind: state.EdgeRuleKindHeaders,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
				ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-From", Value: a.Slug, Action: "set"}}}},
		})
		if err != nil {
			t.Fatal(a.Slug, err)
		}
		return rule.ID
	}
	orgARule := mk(orgAApp)
	foreignOrgRule := mk(orgBApp)       // another org, same creator
	foreignPersonalRule := mk(personal) // the creator's personal account

	owner := func(a state.App) string {
		if a.OrgID != "" {
			return a.OrgID
		}
		return member.ID
	}
	ids := func(a state.App) map[string]bool {
		t.Helper()
		rules, err := s.OwnedEdgeRulesForHost(ctx, host, owner(a), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		set, complete, err := s.OwnerEdgeRules(ctx, owner(a), 100)
		if err != nil || !complete {
			t.Fatalf("OwnerEdgeRules: complete=%v err=%v", complete, err)
		}
		got := map[string]bool{}
		for _, r := range rules {
			got[r.ID] = true
		}
		covered := map[string]bool{}
		for _, r := range set {
			covered[r.ID] = true
		}
		for id := range got {
			if !covered[id] {
				t.Errorf("OwnerEdgeRules omitted %s that OwnedEdgeRulesForHost returned", id)
			}
		}
		return got
	}
	orgAGot := ids(orgAApp)
	if !orgAGot[orgARule] {
		t.Error("org A host lost its own rule")
	}
	if orgAGot[foreignOrgRule] {
		t.Error("org A host read org B's rule (same creator)")
	}
	if orgAGot[foreignPersonalRule] {
		t.Error("org A host read the creator's personal-account rule")
	}
	personalGot := ids(personal)
	if !personalGot[foreignPersonalRule] {
		t.Error("personal host lost its own rule")
	}
	if personalGot[orgARule] || personalGot[foreignOrgRule] {
		t.Error("personal host read an org rule of the same creator")
	}
}
