package api

import "testing"

func TestMatchEdgeRulePath(t *testing.T) {
	for _, tc := range []struct {
		glob, path string
		want       bool
	}{
		{"", "/anything", true},
		{"*", "/anything/deep", true},
		{"/api/*", "/api/v1", true},
		{"/api/*", "/api/v1/users", true},
		{"/api/*", "/api/v1/users/7/", true},
		{"/api/*", "/api/", true},
		{"/api/*", "/api", false},
		{"/api/*", "/apix/v1", false},
		{"/api/*", "/v2/api/x", false},
		{"/admin", "/admin", true},
		{"/admin", "/admin/x", false},
		{"/tenants/*/admin/*", "/tenants/acme/admin/users/7", true},
		{"/tenants/*/admin/*", "/tenants/acme/public/users/7", false},
		{"/*.json", "/data.json", true},
		{"/*.json", "/nested/data.json", false},
		{"/**", "/", true},
		{"/**", "/mcp", true},
		{"/**", "/mcp/messages/7", true},
		{"/api/**", "/api", true},
		{"/api/**", "/api/v1/users/7", true},
		{"/api/**", "/apix/v1", false},
		{"/api/**", "/v2/api/x", false},
		{"/tenants/*/admin/**", "/tenants/acme/admin", true},
		{"/tenants/*/admin/**", "/tenants/acme/admin/users/7", true},
		{"/tenants/*/admin/**", "/tenants/acme/public/users/7", false},
	} {
		got, err := MatchEdgeRulePath(tc.glob, tc.path)
		if err != nil || got != tc.want {
			t.Errorf("MatchEdgeRulePath(%q, %q) = %v, %v; want %v", tc.glob, tc.path, got, err, tc.want)
		}
	}
}

func TestEdgeRuleHostMatchesAndKindPath(t *testing.T) {
	for _, tc := range []struct {
		pattern, host string
		want          bool
	}{
		{"*", "anything.example.com", true},
		{"API.example.com", "api.EXAMPLE.com", true},
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "example.com", false},
		{"api.example.com", "api.example.net", false},
		{"", "a.example.com", false},
	} {
		if got := EdgeRuleHostMatches(tc.pattern, tc.host); got != tc.want {
			t.Errorf("EdgeRuleHostMatches(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
	if ok, _ := MatchEdgeRuleKindPath("maintenance", "/admin/*", "/ADMIN/x"); !ok {
		t.Error("protective kind did not match a case variant")
	}
	if ok, _ := MatchEdgeRuleKindPath("headers", "/admin/*", "/ADMIN/x"); ok {
		t.Error("non-protective kind widened to a case variant")
	}
}
