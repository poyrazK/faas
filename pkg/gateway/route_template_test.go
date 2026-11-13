// adr: 242
package gateway

import (
	"strings"
	"testing"
)

func TestInferredObservedPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/profile/238", "/profile/{id}"},
		{"/profile/392", "/profile/{id}"},
		{"/orders/550e8400-e29b-41d4-a716-446655440000", "/orders/{id}"},
		{"/users/me", "/users/me"},
		{"/v1/products", "/v1/products"},
		{"/2024", "/2024"},
		{"/objects/0123456789abcdef01234567", "/objects/{id}"},
		{"/" + strings.Repeat("x", 249), otherRouteLabel},
		// ADR-20261028110044 amendment 7: typed IDs, dates and ID-named files.
		{"/v1/exports/exports/2026-11-07/qry_cc7569fa5a1a2632.json.gz", "/v1/exports/exports/{date}/{id}.json.gz"},
		{"/customers/cus_N2kQ8XyZ4a", "/customers/{id}"},
		{"/events/evt-01HZX3K9QJ7B2M", "/events/{id}"},
		{"/reports/2026-11-07", "/reports/{date}"},
		{"/streams/edge-prod-1zrhb/ingest", "/streams/edge-prod-1zrhb/ingest"},
		{"/users/user-settings", "/users/user-settings"},
		{"/assets/app.js", "/assets/app.js"},
		{"/assets/styles.css", "/assets/styles.css"},
	} {
		if got := inferredObservedPath(tc.in); got != tc.want {
			t.Errorf("inferredObservedPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// adr: 244
func TestObservedRouteLabelRejectsOutboxPoisonPaths(t *testing.T) {
	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/users/{id}", "GET /users/{id}"},
		{"GET", "/users/private?token", otherRouteLabel},
		{"GET", "/users/name\nother", otherRouteLabel},
		{"GET", "/users/name\x00other", otherRouteLabel},
		{"LONGCUSTOMMETHODNAME", "/users", otherRouteLabel},
	} {
		if got := observedRouteLabel(tc.method, tc.path); got != tc.want {
			t.Errorf("observedRouteLabel(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}
