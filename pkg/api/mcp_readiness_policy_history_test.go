package api

import (
	"encoding/json"
	"testing"
)

func TestMCPReadinessPolicyConfigurationDiff(t *testing.T) {
	from := MCPReadinessPolicy{Scope: "staging", Revision: "old", Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Roles: []MCPReadinessRole{{Name: "z", CatalogSHA256: "old"}, {Name: "a", Monitor: &MCPReadinessMonitorConfig{OAuthConnection: &MCPReadinessOAuthConnection{Role: "reader", Revision: "old"}}}}}
	to := from
	to.Revision = "new"
	to.RestoredFrom = "unrelated"
	to.Reason = "new audit"
	to.Roles = []MCPReadinessRole{from.Roles[1], from.Roles[0]}
	if changes := DiffMCPReadinessPolicies(from, to).Changes; len(changes) != 0 {
		t.Fatal("audit or ordering changed configuration", changes)
	}
	to.Endpoint = "/staging-mcp"
	to.MaxAgeSeconds = 60
	to.Roles = []MCPReadinessRole{{Name: "a", Monitor: &MCPReadinessMonitorConfig{OAuthConnection: &MCPReadinessOAuthConnection{Role: "reader", Revision: "new"}}}, {Name: "b", CatalogSHA256: "added"}}
	diff := DiffMCPReadinessPolicies(from, to)
	paths := []string{"/endpoint", "/max_age_seconds", "/roles/a", "/roles/b", "/roles/z"}
	if diff.Scope != "staging" || len(diff.Changes) != len(paths) {
		t.Fatal(diff)
	}
	for i, p := range paths {
		if diff.Changes[i].Path != p || !json.Valid(diff.Changes[i].Before) || !json.Valid(diff.Changes[i].After) {
			t.Fatal(diff)
		}
	}
	if string(diff.Changes[3].Before) != "null" || string(diff.Changes[4].After) != "null" {
		t.Fatal("add/remove not represented", diff)
	}
}
