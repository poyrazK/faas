package state

import "testing"

// adr: 091 — the MCP access-policy fingerprint covers the JWT rules the
// gateway can apply to the app's hosts, the set the CLI reads from
// GET /v1/apps/{slug}/edge-rules?covering=true (amendment 2026-11-02, review
// round 8): the app's own rules, and its live same-scope siblings' rules other
// than "@app" ones.
func TestMCPAccessPolicyRulesAreTheOnesTheGatewayApplies(t *testing.T) {
	m := NewMemStore()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orgs["co"] = Org{ID: "co"}
	m.orgs["personal"] = Org{ID: "personal", Personal: true}
	m.apps["mcp"] = App{ID: "mcp", AccountID: "acct", OrgID: "co"}
	m.apps["sibling"] = App{ID: "sibling", AccountID: "acct", OrgID: "co"}
	m.apps["gone"] = App{ID: "gone", AccountID: "acct", OrgID: "co", Status: AppDeleted}
	m.apps["mine"] = App{ID: "mine", AccountID: "acct", OrgID: "personal"}
	scope := m.edgeRuleScopeLocked(m.apps["mcp"])
	for _, tc := range []struct {
		name string
		rule EdgeRule
		want bool
	}{
		{"own app-wide rule", EdgeRule{AppID: "mcp", MatchHost: "@app"}, true},
		{"sibling host rule", EdgeRule{AppID: "sibling", MatchHost: "*"}, true},
		{"sibling app-wide rule", EdgeRule{AppID: "sibling", MatchHost: "@app"}, false},
		{"deleted sibling", EdgeRule{AppID: "gone", MatchHost: "*"}, false},
		{"creator's personal app", EdgeRule{AppID: "mine", MatchHost: "*"}, false},
	} {
		if got := m.mcpAccessRuleAppliesLocked(tc.rule, "mcp", scope); got != tc.want {
			t.Errorf("%s: applies = %v, want %v", tc.name, got, tc.want)
		}
	}
}
