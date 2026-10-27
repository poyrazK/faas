package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type ownedEdgeRulesTestStore interface {
	CreateEdgeRule(ctx context.Context, p state.CreateEdgeRuleParams) (state.EdgeRule, error)
	state.OwnedEdgeRuleStore
	state.OwnerEdgeRuleSetStore
}

// adr: 091 — amendment 2026-10-26: OwnedEdgeRulesForHost returns only the
// owning account's enabled rules covering the host, plus the serving app's
// "@app" rules, in evaluation order; another account's "*" rules never ride
// along.
func checkOwnedEdgeRulesForHost(t *testing.T, ctx context.Context, s ownedEdgeRulesTestStore, acctA, appA, appA2, acctB, appB, host string) {
	t.Helper()
	action := state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Owned", Value: "1", Action: "set"}}}}
	mk := func(acct, app, matchHost string, priority int, enabled bool) string {
		t.Helper()
		r, err := s.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct, AppID: app, MatchHost: matchHost,
			MatchPath: "/**", MatchMethods: []string{}, Priority: priority, Enabled: enabled, Kind: state.EdgeRuleKindHeaders, Action: action})
		if err != nil {
			t.Fatalf("CreateEdgeRule(%s): %v", matchHost, err)
		}
		return r.ID
	}
	star := mk(acctA, appA, "*", 30, true)
	wildcard := mk(acctA, appA, "*."+host[len("shop."):], 20, true)
	exact := mk(acctA, appA, host, 10, true)
	appWide := mk(acctA, appA, api.EdgeRuleAppHosts, 15, true)
	mk(acctA, appA, host, 5, false)                 // disabled
	mk(acctA, appA2, api.EdgeRuleAppHosts, 1, true) // another app's @app
	mk(acctA, appA, "other."+host, 1, true)         // another host
	foreignStar := mk(acctB, appB, "*", 1, true)    // another account's "*"
	foreignExact := mk(acctB, appB, host, 2, true)  // another account, same host

	ids := func(accountID, appID string) []string {
		t.Helper()
		rules, err := s.OwnedEdgeRulesForHost(ctx, host, accountID, appID)
		if err != nil {
			t.Fatalf("OwnedEdgeRulesForHost: %v", err)
		}
		out := make([]string, len(rules))
		for i, r := range rules {
			if r.AccountID != accountID {
				t.Fatalf("rule %s of account %s returned for owner %s", r.ID, r.AccountID, accountID)
			}
			out[i] = r.ID
		}
		return out
	}
	equal := func(name string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	equal("owner A serving app A", ids(acctA, appA), []string{exact, appWide, wildcard, star})
	equal("owner A, no app", ids(acctA, ""), []string{exact, wildcard, star})
	equal("owner B serving app B", ids(acctB, appB), []string{foreignStar, foreignExact})

	// ADR-091 amendment 2026-10-27: the owner's whole set, filtered per host,
	// is what the per-host read returns.
	set, complete, err := s.OwnerEdgeRules(ctx, acctA, 100)
	if err != nil || !complete || len(set) != 6 {
		t.Fatalf("OwnerEdgeRules = %d rules, complete=%v, err=%v; want account A's 6 enabled rules", len(set), complete, err)
	}
	fromSet := func(host, appID string) []string {
		var out []string
		for _, r := range state.EdgeRulesCoveringHost(set, host, appID, time.Now()) {
			out = append(out, r.ID)
		}
		return out
	}
	equal("owner set, app A", fromSet(host, appA), ids(acctA, appA))
	equal("owner set, no app", fromSet(host, ""), ids(acctA, ""))
	if _, complete, err := s.OwnerEdgeRules(ctx, acctA, 5); err != nil || complete {
		t.Fatalf("OwnerEdgeRules over its limit: complete=%v err=%v, want incomplete", complete, err)
	}
}

func TestMemStoreOwnedEdgeRulesForHost(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	acctA, _ := m.CreateAccount(ctx, "owned-a@example.test", api.PlanPro)
	acctB, _ := m.CreateAccount(ctx, "owned-b@example.test", api.PlanPro)
	appA, _ := m.CreateApp(ctx, state.App{AccountID: acctA.ID, Slug: "owned-shop", Status: state.AppActive})
	appA2, _ := m.CreateApp(ctx, state.App{AccountID: acctA.ID, Slug: "owned-blog", Status: state.AppActive})
	appB, _ := m.CreateApp(ctx, state.App{AccountID: acctB.ID, Slug: "owned-other", Status: state.AppActive})
	checkOwnedEdgeRulesForHost(t, ctx, m, acctA.ID, appA.ID, appA2.ID, acctB.ID, appB.ID, "shop.owned.example.com")
}

func TestPgStoreOwnedEdgeRulesForHost(t *testing.T) {
	s, ctx := pgStore(t)
	acctA, appA := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "owned-a")
	_, appA2 := pgEdgeRuleSeedAccountApp(t, s, ctx, acctA, "owned-a2")
	acctB, appB := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "owned-b")
	checkOwnedEdgeRulesForHost(t, ctx, s, acctA, appA, appA2, acctB, appB, "shop.owned-pg.example.com")
}

// pgEdgeRuleSeedAccountApp adds another app to an existing account.
func pgEdgeRuleSeedAccountApp(t *testing.T, s *state.PgStore, ctx context.Context, acctID, suffix string) (string, string) {
	t.Helper()
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acctID, Slug: "edge-rules-" + suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp(%s): %v", suffix, err)
	}
	return acctID, app.ID
}
