package api

import "testing"

// adr: 091 — amendment 2026-11-07, later: match_host refuses the characters
// the store's LIKE and the gateway's path.Match read differently.
func TestValidateEdgeRuleMatchHostRefusesMatcherDisagreements(t *testing.T) {
	for _, host := range []string{"%", "shop%.example.com", "my_app.example.com", "app[12].example.com", "app].example.com", `app\.example.com`} {
		if prob := ValidateEdgeRuleMatchHost(host); prob == nil {
			t.Errorf("ValidateEdgeRuleMatchHost(%q) accepted a host the two matchers read differently", host)
		}
	}
	for _, host := range []string{"*", "*.example.com", "api.example.com", "app?.example.com", "@app", "xn--bcher-kva.example"} {
		if prob := ValidateEdgeRuleMatchHost(host); prob != nil {
			t.Errorf("ValidateEdgeRuleMatchHost(%q) = %v, want ok", host, prob)
		}
	}
}
