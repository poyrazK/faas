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
	} {
		got, err := MatchEdgeRulePath(tc.glob, tc.path)
		if err != nil || got != tc.want {
			t.Errorf("MatchEdgeRulePath(%q, %q) = %v, %v; want %v", tc.glob, tc.path, got, err, tc.want)
		}
	}
}

func TestOpenAPIPathGlob(t *testing.T) {
	for template, want := range map[string]string{
		"/users":                 "/users",
		"/users/{id}":            "/users/?*",
		"/orders/{id}/items/{n}": "/orders/?*/items/?*",
		"/files/{name}.json":     "/files/?*.json",
		"/literal/*star?":        `/literal/\*star\?`,
	} {
		if got, ok := OpenAPIPathGlob(template); !ok || got != want {
			t.Errorf("OpenAPIPathGlob(%q) = %q, %v; want %q", template, got, ok, want)
		}
	}
	for _, bad := range []string{"/users/{id", "/users/}", "/a/{}", "/a/{b/c}", "users"} {
		if got, ok := OpenAPIPathGlob(bad); ok {
			t.Errorf("OpenAPIPathGlob(%q) = %q, accepted", bad, got)
		}
	}
}

// A trailing parameter must match exactly one non-empty segment: "/*" would
// take MatchEdgeRulePath's subtree branch and cover nested routes too.
func TestOpenAPIPathGlobMatchesOneSegment(t *testing.T) {
	glob, _ := OpenAPIPathGlob("/users/{id}")
	for requestPath, want := range map[string]bool{
		"/users/7": true, "/users/abc": true, "/users/": false, "/users/7/avatar": false, "/users": false,
	} {
		if got, err := MatchEdgeRulePath(glob, requestPath); err != nil || got != want {
			t.Errorf("MatchEdgeRulePath(%q, %q) = %v, %v; want %v", glob, requestPath, got, err, want)
		}
	}
}

func TestEdgeRuleTemplatedPath(t *testing.T) {
	for matchPath, want := range map[string]string{
		"/users/{id}":        "/users/?*",
		"/api/*/users/{id}":  "/api/*/users/?*", // existing glob syntax kept
		"/orders/{id}/items": "/orders/?*/items",
	} {
		if got, ok := EdgeRuleTemplatedPath(matchPath); !ok || got != want {
			t.Errorf("EdgeRuleTemplatedPath(%q) = %q, %v; want %q", matchPath, got, ok, want)
		}
	}
	for _, plain := range []string{"/users", "/api/*", "/a/{", "/a/{}", ""} {
		if got, ok := EdgeRuleTemplatedPath(plain); ok {
			t.Errorf("EdgeRuleTemplatedPath(%q) = %q, want not templated", plain, got)
		}
	}
}
