package api

import (
	"encoding/json"
	"testing"
)

// adr: 091 — a stored WAF or challenge value outside this release's bounds
// is reported as unenforceable, as the gateway fails its hosts closed
// (amendment 2026-11-03, review round 9).
func TestEdgeRuleEnforcementProblem(t *testing.T) {
	tests := []struct {
		name, kind, action string
		enabled, want      bool
	}{
		{"valid waf", "waf", `{"waf":{"mode":"observe","paranoia_level":1}}`, true, false},
		{"waf paranoia out of range", "waf", `{"waf":{"paranoia_level":9}}`, true, true},
		{"disabled rule is not enforced at all", "waf", `{"waf":{"paranoia_level":9}}`, false, false},
		{"valid challenge", "challenge", `{"challenge":{"difficulty":16}}`, true, false},
		{"challenge difficulty out of range", "challenge", `{"challenge":{"difficulty":200}}`, true, true},
		{"other kinds are not checked here", "ip", `{"ip":{"deny":["203.0.113.0/24"]}}`, true, false},
		{"malformed action", "waf", `{`, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EdgeRuleEnforcementProblem(tc.kind, tc.enabled, json.RawMessage(tc.action))
			if (got != "") != tc.want {
				t.Fatalf("problem = %q, want a problem %v", got, tc.want)
			}
		})
	}
}
