package routesplit_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routesplit"
	"github.com/onebox-faas/faas/pkg/state"
)

// doneSplit applies the fixture's split and steps it to done.
func (f *fixture) doneSplit(t *testing.T) state.RouteSplit {
	t.Helper()
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDone {
		t.Fatalf("split = %+v", got)
	}
	return f.read(t, split.ID)
}

func (f *fixture) splitRule(t *testing.T, appID string) state.EdgeRule {
	t.Helper()
	rules, err := f.store.ListEdgeRulesForApp(f.ctx, appID)
	if err != nil || len(rules) == 0 {
		t.Fatalf("rules of %s = %+v, %v", appID, rules, err)
	}
	return rules[0]
}

func TestRuleChangesThatWouldUndoALiveSplit(t *testing.T) {
	f := newFixture(t)
	split := f.doneSplit(t)
	route := f.splitRule(t, f.source.ID)
	block := f.splitRule(t, split.TargetAppID)
	source := func(name string, kind state.EdgeRuleKind, host, path string, enabled bool) *state.EdgeRule {
		return &state.EdgeRule{AccountID: f.acct.ID, AppID: f.source.ID, Name: name, Kind: kind, MatchHost: host, MatchPath: path, Enabled: enabled}
	}
	disabled := route
	disabled.Enabled = false
	cases := []struct {
		name          string
		before, after *state.EdgeRule
		code          string
	}{
		{"delete the route rule", &route, nil, api.CodeRouteSplitRuleConflict},
		{"disable the route rule", &route, &disabled, api.CodeRouteSplitRuleConflict},
		{"delete the block", &block, nil, api.CodeRouteSplitRuleConflict},
		{"take a split's rule name", nil, source(routesplit.RuleName("x"), state.EdgeRuleKindIP, "shop.example", "/**", true), api.CodeValidation},
		{"app-wide rule on the source", nil, source("", state.EdgeRuleKindIP, api.EdgeRuleAppHosts, "/admin/**", true), api.CodeRouteSplitRuleConflict},
		{"redirect over the moved path", nil, source("", state.EdgeRuleKindRedirect, "shop.example", "/exports/**", true), api.CodeRouteSplitRuleConflict},
		{"rate limit over the moved path", nil, source("", state.EdgeRuleKindThrottle, "shop.example", "/**", true), api.CodeRouteSplitRuleConflict},
		{"disabled redirect over the moved path", nil, source("", state.EdgeRuleKindRedirect, "shop.example", "/exports/**", false), ""},
		{"redirect elsewhere", nil, source("", state.EdgeRuleKindRedirect, "shop.example", "/old/**", true), ""},
		{"host ip rule over the moved path", nil, source("", state.EdgeRuleKindIP, "shop.example", "/**", true), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prob := f.svc.RuleChangeProblem(f.ctx, tc.before, tc.after)
			switch {
			case tc.code == "" && prob != nil:
				t.Fatalf("refused: %+v", prob)
			case tc.code != "" && (prob == nil || prob.Code != tc.code):
				t.Fatalf("problem = %+v, want %s", prob, tc.code)
			}
		})
	}
	if prob := f.svc.RuleChangeProblem(routesplit.Acting(f.ctx), &route, nil); prob != nil {
		t.Fatalf("the split's own change was refused: %+v", prob)
	}
	if prob := f.svc.RuleChangeProblem(f.ctx, &route, nil); prob == nil || !strings.Contains(prob.Detail, "--undo-split "+split.ID) {
		t.Fatalf("refusal does not say how to undo: %+v", prob)
	}
}

func TestRuleChangesAfterUndoAreFree(t *testing.T) {
	f := newFixture(t)
	split := f.doneSplit(t)
	route := f.splitRule(t, f.source.ID)
	if _, prob := f.svc.Undo(f.ctx, f.acct, f.source, split); prob != nil {
		t.Fatalf("undo: %+v", prob)
	}
	wide := &state.EdgeRule{AccountID: f.acct.ID, AppID: f.source.ID, Kind: state.EdgeRuleKindIP, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Enabled: true}
	if prob := f.svc.RuleChangeProblem(f.ctx, nil, wide); prob != nil {
		t.Fatalf("rule refused after undo: %+v", prob)
	}
	if prob := f.svc.RuleChangeProblem(f.ctx, &route, nil); prob != nil {
		t.Fatalf("an undone split's leftover rule could not be removed: %+v", prob)
	}
}
