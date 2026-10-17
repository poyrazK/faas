package speccompliance

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Catch crossing static/wildcard issue routes without compiling the APID server.
func TestNativeResponseInboxRouteCompatibility(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := scanServerRoutes(filepath.Join(root, "pkg/apid/apidserver", serverSrcPath))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, route := range routes {
		if strings.Contains(route, " /dashboard/apps/{slug}/issues") {
			mux.HandleFunc(route, func(http.ResponseWriter, *http.Request) {})
		}
	}
	for _, tc := range []struct{ method, path, pattern string }{
		{"GET", "/dashboard/apps/app/issues/responses", "GET /dashboard/apps/{slug}/issues/responses"},
		{"POST", "/dashboard/apps/app/issues/issue/triage", "POST /dashboard/apps/{slug}/issues/{issue_id}/triage"},
		{"POST", "/dashboard/apps/app/issues/issue/split-rules/rule", "POST /dashboard/apps/{slug}/issues/{issue_id}/split-rules/{rule_id}"},
	} {
		_, pattern := mux.Handler(httptest.NewRequest(tc.method, tc.path, nil))
		if pattern != tc.pattern {
			t.Fatal(tc, pattern)
		}
	}
}
