// adr: 20261023115741 — gatewayd-internal drops an off capability's edge
// rules per request, by the account that owns each rule.
package main

import (
	"slices"
	"testing"
)

func TestCapabilityRulesDropsOnlyTheOffAccountsRules(t *testing.T) {
	type rule struct{ id, account string }
	rules := []rule{{"a", "on"}, {"b", "off"}, {"c", "on"}, {"d", "off"}}
	account := func(r *rule) string { return r.account }
	allowed := func(capability, accountID string) bool {
		return capability != capabilityAsyncRoutes || accountID != "off"
	}

	tests := []struct {
		name       string
		allowed    func(string, string) bool
		capability string
		want       []string
	}{
		{"nil decider keeps every rule", nil, capabilityAsyncRoutes, []string{"a", "b", "c", "d"}},
		{"capability on keeps every rule", allowed, capabilityResponseCaching, []string{"a", "b", "c", "d"}},
		{"capability off for one account", allowed, capabilityAsyncRoutes, []string{"a", "c"}},
		{"capability off for everyone", func(string, string) bool { return false }, capabilityAsyncRoutes, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(rules)
			got := capabilityRules(tc.allowed, tc.capability, rules, account)
			ids := []string{}
			for _, r := range got {
				ids = append(ids, r.id)
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("kept %v, want %v", ids, tc.want)
			}
			if !slices.Equal(rules, before) {
				t.Fatalf("capabilityRules modified the cached slice: %v", rules)
			}
		})
	}
}
