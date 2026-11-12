package edgerules

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-28 (review round 3): a rollback runs the
// checks a new rule passes on every rule it would restore. Only the plan's
// rule counts were checked, so a rollback could bring back a rule whose list
// had been deleted (it then never matched), a kind the plan no longer allows,
// or a feature the account's release no longer has.
func TestRestoreProblemRunsCreateTimeChecks(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	free, err := mem.CreateAccount(ctx, "free@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	pro, err := mem.CreateAccount(ctx, "pro@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: pro.ID, Name: "office", Kind: "ip", Items: []string{"203.0.113.1"}}, 10); err != nil {
		t.Fatal(err)
	}
	ipRule := func(account string, match *api.EdgeRuleMatchExpr) state.EdgeRule {
		return state.EdgeRule{ID: "rule-ip", AccountID: account, Kind: state.EdgeRuleKindIP, Enabled: true, MatchHost: "@app", MatchPath: "/**",
			Match: match, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"0.0.0.0/0"}}}}
	}
	challenge := state.EdgeRule{ID: "rule-challenge", AccountID: pro.ID, Kind: state.EdgeRuleKindChallenge, Enabled: true, MatchHost: "@app", MatchPath: "/**"}
	// adr: 091 — amendment 2026-11-07, later: a credentialed wildcard under a
	// public suffix, recorded before the guard, is not restored over a fix.
	corsRule := func(origin string) state.EdgeRule {
		return state.EdgeRule{ID: "rule-cors", AccountID: pro.ID, Kind: state.EdgeRuleKindCORSA, Enabled: true, MatchHost: "@app", MatchPath: "/**",
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{AllowOrigins: []string{origin}, AllowMethods: []string{"GET"}, AllowCredentials: true}}}
	}
	riskyCORS, fixedCORS := corsRule("https://*.vercel.app"), corsRule("https://app.example.com")
	for _, tc := range []struct {
		name       string
		acct       state.Account
		restored   state.EdgeRule
		current    []state.EdgeRule
		unreleased map[string]bool
		wantStatus int
		wantDetail string
	}{
		{name: "a deleted list", acct: pro, restored: ipRule(pro.ID, &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "gone"}),
			wantStatus: http.StatusBadRequest, wantDetail: `"gone"`},
		{name: "an existing list", acct: pro, restored: ipRule(pro.ID, &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"})},
		{name: "a kind the plan no longer allows", acct: free, restored: ipRule(free.ID, nil),
			wantStatus: http.StatusPaymentRequired, wantDetail: "rule-ip"},
		{name: "a match method outside the vocabulary", acct: pro,
			restored:   func() state.EdgeRule { r := ipRule(pro.ID, nil); r.MatchMethods = []string{"*"}; return r }(),
			wantStatus: http.StatusBadRequest, wantDetail: "match_methods"},
		{name: "an unreleased feature", acct: pro, restored: challenge, unreleased: map[string]bool{featureChallenge.flag: true},
			wantStatus: http.StatusUnprocessableEntity, wantDetail: "rule-challenge"},
		{name: "an unreleased feature the rule already enforces", acct: pro, restored: challenge, current: []state.EdgeRule{challenge},
			unreleased: map[string]bool{featureChallenge.flag: true}},
		{name: "a credentialed public-suffix CORS wildcard over a fix", acct: pro, restored: riskyCORS, current: []state.EdgeRule{fixedCORS},
			wantStatus: http.StatusBadRequest, wantDetail: "public suffix"},
		{name: "the same CORS wildcard already live", acct: pro, restored: riskyCORS, current: []state.EdgeRule{riskyCORS}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{Host: attackModeHost{store: mem, audits: &[]string{}, unreleased: tc.unreleased}}
			prob := svc.restoreProblem(ctx, tc.acct, state.App{ID: "app-1", AccountID: tc.acct.ID}, []state.EdgeRule{tc.restored}, tc.current)
			if tc.wantStatus == 0 {
				if prob != nil {
					t.Fatalf("restoreProblem = %+v, want none", prob)
				}
				return
			}
			if prob == nil || prob.Status != tc.wantStatus || !strings.Contains(prob.Detail, tc.wantDetail) {
				t.Fatalf("restoreProblem = %+v, want status %d with %q in the detail", prob, tc.wantStatus, tc.wantDetail)
			}
		})
	}
}
