package api

import (
	"strings"
	"testing"
)

// TestValidateReleaseProbePaths pins ADR-1449's rule: up to three distinct,
// concrete absolute paths under ADR-847's probe constraints.
func TestValidateReleaseProbePaths(t *testing.T) {
	for _, ok := range [][]string{nil, {}, {"/"}, {"/healthz", "/api/orders", "/v1/ping"}} {
		if err := ValidateReleaseProbePaths(ok); err != nil {
			t.Errorf("ValidateReleaseProbePaths(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range [][]string{
		{"healthz"}, {"/a?b=1"}, {"/a#top"}, {"/users/{id}"}, {"/a*"}, {"/a b"}, {"/a\\b"},
		{"/" + strings.Repeat("a", RouteHealthMaxPathBytes)},
		{"/x", "/x"}, {"/a", "/b", "/c", "/d"},
	} {
		if err := ValidateReleaseProbePaths(bad); err == nil {
			t.Errorf("ValidateReleaseProbePaths(%q) = nil, want an error", bad)
		}
	}
}

func TestAppManifestValidatesReleaseProbePaths(t *testing.T) {
	m := AppManifest{Entrypoint: []string{"/app"}, ReleaseProbePaths: []string{"/ok", "no-slash"}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "release_probe_paths") {
		t.Fatalf("Validate() = %v, want a release_probe_paths error", err)
	}
}
