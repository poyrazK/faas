// router_test.go — pin IsApidPath's anchored-root discipline.
//
// The matcher is the load-bearing seam between `gatewayd-internal`'s
// apidProxy and the apid loopback; a regression silently steals
// customer-app traffic (e.g. "/v1.zip" or "/loginfoo" routed to
// apid instead of the wake/proxy path). This table pins every
// branch plus the anchored-root anti-shadowing cases from PR #180
// review finding #6.
package apid

import "testing"

// TestIsApidPath exercises the anchored-root discipline and the
// subtree-only /oauth/* path. The table includes both positive
// (must-route-to-apid) and negative (must-fall-through) cases.
func TestIsApidPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// ─── positive: anchored-root exact matches ─────────────────
		{"/v1", true},
		{"/v1/", true},
		{"/v1/apps", true},
		{"/v1/apps/foo/deployments", true},
		{"/dashboard", true},
		{"/dashboard/", true},
		{"/dashboard/account", true},
		{"/login", true},
		{"/login/forgot", true},
		{"/signup", true},
		{"/auth/verify", true},
		{"/auth/reset", true},
		{"/logout", true},
		{"/status", true},
		{"/healthz", false},
		{"/cli-auth", true},
		{"/docs", true},
		{"/docs/", true},
		{"/docs/assets/swagger-ui.css", true},
		// ─── positive: /oauth/* subtree only ────────────────────────
		{"/oauth/google/cb", true},
		{"/oauth/github/cb", true},
		{"/oauth/x", true},
		// ─── negative: bare /oauth (no exact match) ─────────────────
		{"/oauth", false},
		// ─── negative: anchored-root shadowing (review #6) ─────────
		{"/v1.zip", false},
		{"/dashboardfoo", false},
		{"/loginfoo", false},
		{"/logoutfoo", false},
		{"/signupfoo", false},
		{"/statusfoo", false},
		{"/healthzfoo", false},
		{"/cli-authfoo", false},
		{"/docsfoo", false},
		{"/auth/verifyfoo", false},
		{"/auth/resetfoo", false},
		// ─── negative: customer-app paths (must fall through) ──────
		{"/", false},
		{"", false},
		{"/api", false},
		{"/apps", false},
		{"/myapp.example.com", false},
		{"/v2", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsApidPath(tc.path); got != tc.want {
				t.Errorf("IsApidPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestHasApidPrefix pins the anchored-root primitive directly so a
// regression in IsApidPath that's masked by the outer loop is still
// caught here.
func TestHasApidPrefix(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         bool
	}{
		// exact matches
		{"/v1", "/v1", true},
		{"/v1/", "/v1", true},
		// subtree match
		{"/v1/apps", "/v1", true},
		{"/v1/apps/foo", "/v1", true},
		// shadowing (must NOT match)
		{"/v1.zip", "/v1", false},
		{"/v12", "/v1", false},
		{"/dashboardfoo", "/dashboard", false},
		// unrelated
		{"/oauth", "/v1", false},
		{"", "/v1", false},
		{"/v1", "", true}, // empty prefix is a degenerate match-everything
	}
	for _, tc := range cases {
		t.Run(tc.path+"_"+tc.prefix, func(t *testing.T) {
			if got := hasApidPrefix(tc.path, tc.prefix); got != tc.want {
				t.Errorf("hasApidPrefix(%q, %q) = %v, want %v", tc.path, tc.prefix, got, tc.want)
			}
		})
	}
}
