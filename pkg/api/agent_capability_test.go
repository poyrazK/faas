package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261027090016 amendment 1 — a policy allows a tool only in allow
// mode, and a run's tool-call budget never passes its plan's ceiling.
func TestAgentCapabilityPolicy(t *testing.T) {
	var none *api.AgentCapabilityPolicy
	if none.Allows(api.AgentCapabilityWebFetch) {
		t.Error("no policy allows web_fetch")
	}
	p := &api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: api.AgentCapabilityWebFetch, Mode: api.AgentCapabilityDeny}}}
	if p.Allows(api.AgentCapabilityWebFetch) {
		t.Error("deny allows web_fetch")
	}
	p.Tools[0].Mode = api.AgentCapabilityAllow
	if !p.Allows(api.AgentCapabilityWebFetch) || p.Allows("web_search") {
		t.Error("allow lists only the tools it names")
	}
	// Amendment 2: approve lists the tool but does not allow it outright.
	p.Tools[0].Mode = api.AgentCapabilityApprove
	if p.Allows(api.AgentCapabilityWebFetch) || !p.Lists(api.AgentCapabilityWebFetch) ||
		p.Mode(api.AgentCapabilityWebFetch) != api.AgentCapabilityApprove || p.Mode("web_search") != api.AgentCapabilityDeny ||
		none.Mode(api.AgentCapabilityWebFetch) != api.AgentCapabilityDeny || none.Lists(api.AgentCapabilityWebFetch) {
		t.Error("approve mode: listed, not allowed; unnamed tools deny")
	}
	p.Tools[0].Mode = api.AgentCapabilityAllow
	for _, tc := range []struct {
		policy *api.AgentCapabilityPolicy
		plan   api.Plan
		want   int
	}{
		{nil, api.PlanPro, 5_000},
		{p, api.PlanHobby, 1_000},
		{&api.AgentCapabilityPolicy{Budgets: &api.AgentCapabilityBudgets{ToolCalls: 50}}, api.PlanScale, 50},
		{&api.AgentCapabilityPolicy{Budgets: &api.AgentCapabilityBudgets{ToolCalls: 9_000}}, api.PlanPro, 5_000},
		{nil, api.PlanFree, 0},
	} {
		if got := tc.policy.ToolCallBudget(tc.plan); got != tc.want {
			t.Errorf("%+v on %s: budget %d, want %d", tc.policy, tc.plan, got, tc.want)
		}
	}
}
