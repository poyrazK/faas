package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1191 — AppWideEdgeRules returns only the app's enabled "@app" rules
// in evaluation order, and MergeEdgeRulesInOrder keeps that order.
func TestAppWideEdgeRules(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	acct, _ := m.CreateAccount(ctx, "a@example.test", api.PlanPro)
	app, _ := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	other, _ := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "blog", Status: state.AppActive})
	action := state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X", Value: "1", Action: "set"}}}}
	mk := func(appID, host string, priority int, enabled bool) state.EdgeRule {
		t.Helper()
		r, err := m.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: appID, MatchHost: host, MatchPath: "/**", Priority: priority, Enabled: enabled, Kind: state.EdgeRuleKindHeaders, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	late := mk(app.ID, api.EdgeRuleAppHosts, 20, true)
	early := mk(app.ID, api.EdgeRuleAppHosts, 10, true)
	mk(app.ID, api.EdgeRuleAppHosts, 1, false)
	mk(other.ID, api.EdgeRuleAppHosts, 1, true)
	host := mk(app.ID, "shop.gregale.dev", 15, true)

	got, err := m.AppWideEdgeRules(ctx, app.ID)
	if err != nil || len(got) != 2 || got[0].ID != early.ID || got[1].ID != late.ID {
		t.Fatalf("AppWideEdgeRules = %+v, err %v", got, err)
	}
	forHost, _ := m.MatchEdgeRulesForHost(ctx, "shop.gregale.dev")
	if len(forHost) != 1 || forHost[0].ID != host.ID {
		t.Fatalf("host query must not return @app rules: %+v", forHost)
	}
	merged := state.MergeEdgeRulesInOrder(forHost, got)
	if len(merged) != 3 || merged[0].ID != early.ID || merged[1].ID != host.ID || merged[2].ID != late.ID {
		ids := []string{}
		for _, r := range merged {
			ids = append(ids, r.ID)
		}
		t.Fatalf("merged order = %v", ids)
	}
}
